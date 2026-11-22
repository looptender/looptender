//go:build darwin && cgo

package legacyretire

/*
#cgo LDFLAGS: -lbsm -lproc
#cgo CFLAGS: -Wno-deprecated-declarations
#include <mach/mach.h>
#include <mach/task_info.h>
#include <bsm/libbsm.h>
#include <libproc.h>
#include <sys/event.h>
#include <sys/proc_info.h>
#include <sys/sysctl.h>
#include <signal.h>
#include <errno.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>

static int retirement_token(int pid, uint32_t out[8]) {
 mach_port_t port=MACH_PORT_NULL;
 if(task_name_for_pid(mach_task_self(),pid,&port)!=KERN_SUCCESS) return EPERM;
 audit_token_t token={{0}};
 mach_msg_type_number_t count=TASK_AUDIT_TOKEN_COUNT;
 kern_return_t kr=task_info(port,TASK_AUDIT_TOKEN,(task_info_t)&token,&count);
 kern_return_t release=mach_port_deallocate(mach_task_self(),port);
 if(kr!=KERN_SUCCESS||release!=KERN_SUCCESS||count!=TASK_AUDIT_TOKEN_COUNT) return EPERM;
 if(audit_token_to_pid(token)!=pid||audit_token_to_euid(token)!=geteuid()) return EPERM;
 memcpy(out,&token,sizeof(token));return 0;
}
static int retirement_path(int pid,char *buf,int size) {
 return proc_pidpath(pid,buf,(uint32_t)size);
}
// One fixed-cap read: no size-query allocation and no ENOMEM retry.
static int retirement_arguments(int pid, void *buf, size_t *size) {
 int mib[3]={CTL_KERN,KERN_PROCARGS2,pid};
 if(sysctl(mib,3,buf,size,NULL,0)!=0)return errno;
 return 0;
}
static int retirement_arm(int pid) {
 int fd=kqueue();if(fd<0)return -1;
 struct kevent change,receipt;memset(&receipt,0,sizeof(receipt));
 EV_SET(&change,(uintptr_t)pid,EVFILT_PROC,EV_ADD|EV_RECEIPT,NOTE_EXIT|NOTE_EXEC,0,NULL);
 struct timespec zero={0,0};
 if(kevent(fd,&change,1,&receipt,1,&zero)!=1||!(receipt.flags&EV_ERROR)||receipt.data!=0){close(fd);return -1;}
 return fd;
}
// 0: no event, 1: exact exit, 2: exec or malformed/unavailable event.
static int retirement_event(int fd,int pid,int milliseconds) {
 struct kevent ev;memset(&ev,0,sizeof(ev));
 struct timespec limit={milliseconds/1000,(milliseconds%1000)*1000000L};
 int n=kevent(fd,NULL,0,&ev,1,&limit);
 if(n==0)return 0;
 if(n!=1||ev.ident!=(uintptr_t)pid||ev.filter!=EVFILT_PROC||(ev.flags&EV_ERROR)||(ev.fflags&NOTE_EXEC))return 2;
 return (ev.fflags&NOTE_EXIT)?1:2;
}
static int retirement_signal(uint32_t value[8]) {
 int (*send_token)(audit_token_t*,int)=dlsym(RTLD_DEFAULT,"proc_signal_with_audittoken");
 if(!send_token)return ENOTSUP;
 audit_token_t token;memcpy(&token,value,sizeof(token));
 return send_token(&token,SIGTERM); // direct error number, not -1/errno
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"
)

// Process is fresh native invocation evidence. Executable names the native
// image path, not proof that bytes currently at that path equal mapped bytes.
type Process struct {
	Token      [8]uint32
	Executable string
	Arguments  []string
}

func nativeToken(pid int) ([8]uint32, error) {
	var t [8]uint32
	if pid <= 1 || uint64(pid) > 2147483647 {
		return t, ErrUnavailable
	}
	if rc := C.retirement_token(C.int(pid), (*C.uint32_t)(unsafe.Pointer(&t[0]))); rc != 0 {
		return t, fmt.Errorf("native token unavailable (%d)", int(rc))
	}
	return t, nil
}

// Inspect obtains fresh incarnation identity before and after native argv/image
// reads. Environment bytes returned by the OS are never parsed or retained.
func Inspect(pid int) (Process, error) {
	var p Process
	before, e := nativeToken(pid)
	if e != nil {
		return p, e
	}
	path := make([]byte, 4096)
	n := int(C.retirement_path(C.int(pid), (*C.char)(unsafe.Pointer(&path[0])), C.int(len(path))))
	if n <= 0 || n >= len(path) {
		return p, ErrUnavailable
	}
	p.Executable = strings.TrimRight(string(path[:n]), "\x00")
	raw := make([]byte, 256*1024)
	size := C.size_t(len(raw))
	rc := C.retirement_arguments(C.int(pid), unsafe.Pointer(&raw[0]), &size)
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	if rc != 0 || size < 5 || uint64(size) > uint64(len(raw)) {
		return p, ErrUnavailable
	}
	raw = raw[:int(size)]
	if len(raw) < 5 || len(raw) > 256*1024 {
		return p, ErrUnavailable
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	if argc < 1 || argc > 128 {
		return p, ErrUnavailable
	}
	offset := 4
	for offset < len(raw) && raw[offset] != 0 {
		offset++
	} // executable path
	for offset < len(raw) && raw[offset] == 0 {
		offset++
	}
	for i := 0; i < argc; i++ {
		start := offset
		for offset < len(raw) && raw[offset] != 0 {
			offset++
		}
		if offset >= len(raw) || offset-start > 16384 {
			return Process{}, ErrUnavailable
		}
		p.Arguments = append(p.Arguments, string(raw[start:offset]))
		offset++
	}
	after, e := nativeToken(pid)
	if e != nil || before != after {
		return Process{}, ErrUnavailable
	}
	p.Token = after
	return p, nil
}

type nativeObserver struct {
	fd, pid int
	token   [8]uint32
	sent    bool
}

// ArmNative is usable only after the adapter verifies full application evidence.
// It never registers by PID after dispatch. Missing/mismatched identity refuses.
func ArmNative(ctx context.Context, b Binding) (Observer, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	before, e := nativeToken(b.PID)
	if e != nil || before != b.Token {
		return nil, ErrUnavailable
	}
	fd := int(C.retirement_arm(C.int(b.PID)))
	if fd < 0 {
		return nil, ErrUnavailable
	}
	after, e := nativeToken(b.PID)
	if e != nil || after != before {
		C.close(C.int(fd))
		return nil, ErrUnavailable
	}
	return &nativeObserver{fd: fd, pid: b.PID, token: after}, nil
}
func (n *nativeObserver) Terminate(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if n.fd < 0 || n.sent {
		return ErrSpent
	}
	// Any queued exit/exec invalidates dispatch; absence is not a death proof.
	if C.retirement_event(C.int(n.fd), C.int(n.pid), 0) != 0 {
		return ErrUnavailable
	}
	n.sent = true
	rc := C.retirement_signal((*C.uint32_t)(unsafe.Pointer(&n.token[0])))
	if rc != 0 {
		return fmt.Errorf("token-directed retirement request unavailable (%d); attempt consumed", int(rc))
	}
	return nil
}
func (n *nativeObserver) WaitExit(ctx context.Context) (bool, error) {
	if n.fd < 0 || !n.sent {
		return false, ErrUnavailable
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return false, errors.New("bounded observation required")
	}
	for {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return false, context.DeadlineExceeded
		}
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		event := int(C.retirement_event(C.int(n.fd), C.int(n.pid), C.int(wait.Milliseconds())))
		if event == 1 {
			return true, nil
		}
		if event != 0 {
			return false, ErrUnavailable
		}
	}
}
func (n *nativeObserver) Close() error {
	if n.fd < 0 {
		return nil
	}
	fd := n.fd
	n.fd = -1
	if C.close(C.int(fd)) != 0 {
		return os.ErrInvalid
	}
	return nil
}

// Supported reports whether this build includes the Darwin native backend.
func Supported() bool { return true }

// ArmExitObservation arms a no-signal observation before an external manager
// action. It does not authenticate that manager or authorize the action. Tokens
// must come from fresh separately authenticated selection, never a saved PID.
// API/kernel calls are cooperative; context deadlines cannot interrupt a stall.
func ArmExitObservation(ctx context.Context, pid int, expected [8]uint32) (ExitObservation, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrUnavailable
	}
	before, e := nativeToken(pid)
	if e != nil || before != expected {
		return nil, ErrUnavailable
	}
	fd := int(C.retirement_arm(C.int(pid)))
	if fd < 0 {
		return nil, ErrUnavailable
	}
	after, e := nativeToken(pid)
	if e != nil || after != before || ctx.Err() != nil {
		C.close(C.int(fd))
		return nil, ErrUnavailable
	}
	// These two closures have only event-read and owned-descriptor-close effects.
	// Do not return/wrap nativeObserver: it carries termination authority.
	return &exitObservation{
		poll: func(wait time.Duration) int {
			return int(C.retirement_event(C.int(fd), C.int(pid), C.int(wait.Milliseconds())))
		},
		release: func() error {
			if C.close(C.int(fd)) != 0 {
				return os.ErrInvalid
			}
			return nil
		},
	}, nil
}
