// Darwin research fixture only. No production entry point or caller-selected PID.
// Every signal target is a direct child created here; cleanup uses owned pipes.
#include <mach/mach.h>
#include <mach/task_info.h>
#include <bsm/libbsm.h>
#include <dlfcn.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/event.h>
#include <sys/wait.h>
#include <unistd.h>
#include <mach-o/dyld.h>
#include <limits.h>

static int reply_fd = -1;
// Fixed-size diagnostic record; no paths, environment, token bytes or raw output.
static struct {
    int poll_rc, poll_errno, read_rc, read_errno, observed_byte;
    int write_rc, write_errno, queue_rc, queue_errno;
    int acquire_rc, info_rc, release_rc;
    unsigned info_count;
    int event_rc, event_errno;
    unsigned event_flags, event_fflags;
    long event_data;
} diagnostic;
static int request(audit_token_t *t, int (*send_token)(audit_token_t *, int), int *saved_errno) {
    errno = 0;
    int rc = send_token(t, SIGTERM);
    *saved_errno = errno;
    return rc;
}
static int put(int fd, char value) {
    errno = 0;
    diagnostic.write_rc = (int)write(fd, &value, 1);
    diagnostic.write_errno = errno;
    return diagnostic.write_rc == 1;
}
static void term(int sig) {
    (void)sig;
    if (write(reply_fd, "T", 1) != 1) _exit(71);
    _exit(0);
}
static int byte(int fd, char expected) {
    struct pollfd p = {fd, POLLIN, 0};
    char b = 0;
    errno = 0;
    diagnostic.poll_rc = poll(&p, 1, 1500); diagnostic.poll_errno = errno;
    diagnostic.read_rc = -2; diagnostic.read_errno = 0;
    if (diagnostic.poll_rc == 1) {
        errno = 0;
        diagnostic.read_rc = (int)read(fd, &b, 1); diagnostic.read_errno = errno;
    }
    diagnostic.observed_byte = (unsigned char)b;
    return diagnostic.poll_rc == 1 && diagnostic.read_rc == 1 && b == expected;
}
static int token(pid_t pid, audit_token_t *out) {
    mach_port_t port = MACH_PORT_NULL;
    diagnostic.acquire_rc = task_name_for_pid(mach_task_self(), pid, &port);
    if (diagnostic.acquire_rc != KERN_SUCCESS) return 0;
    mach_msg_type_number_t n = TASK_AUDIT_TOKEN_COUNT;
    kern_return_t kr = task_info(port, TASK_AUDIT_TOKEN, (task_info_t)out, &n);
    kern_return_t released = mach_port_deallocate(mach_task_self(), port);
    diagnostic.info_rc = kr; diagnostic.info_count = n; diagnostic.release_rc = released;
    return kr == KERN_SUCCESS && released == KERN_SUCCESS && n == TASK_AUDIT_TOKEN_COUNT &&
        audit_token_to_pid(*out) == pid && audit_token_to_euid(*out) == geteuid();
}
// Receipt proves registration succeeded. Fresh acquisition after registration
// must match the pre-registration incarnation. No late registration after signal.
static int arm(pid_t pid, audit_token_t *bound) {
    audit_token_t after;
    if (!token(pid, bound)) return -1;
    errno = 0;
    int kq = kqueue();
    diagnostic.queue_rc = kq; diagnostic.queue_errno = errno;
    if (kq < 0) return -1;
    struct kevent change, receipt;
    EV_SET(&change, (uintptr_t)pid, EVFILT_PROC, EV_ADD | EV_RECEIPT,
        NOTE_EXIT | NOTE_EXEC, 0, NULL);
    struct timespec zero = {0, 0};
    memset(&receipt, 0, sizeof(receipt)); errno = 0;
    diagnostic.event_rc = kevent(kq, &change, 1, &receipt, 1, &zero);
    diagnostic.event_errno = errno; diagnostic.event_flags = receipt.flags;
    diagnostic.event_fflags = receipt.fflags; diagnostic.event_data = receipt.data;
    if (diagnostic.event_rc != 1 ||
        !(receipt.flags & EV_ERROR) || receipt.data != 0 ||
        !token(pid, &after) || memcmp(bound, &after, sizeof(after))) {
        close(kq); return -1;
    }
    return kq;
}
static int event(int kq, pid_t pid, unsigned wanted, int milliseconds) {
    struct kevent ev;
    struct timespec limit = {milliseconds / 1000, (milliseconds % 1000) * 1000000L};
    memset(&ev, 0, sizeof(ev)); errno = 0;
    int n = kevent(kq, NULL, 0, &ev, 1, &limit);
    diagnostic.event_rc = n; diagnostic.event_errno = errno;
    diagnostic.event_flags = ev.flags; diagnostic.event_fflags = ev.fflags;
    diagnostic.event_data = ev.data;
    if (!wanted) return n == 0;
    return n == 1 && ev.ident == (uintptr_t)pid && ev.filter == EVFILT_PROC &&
        !(ev.flags & EV_ERROR) && (ev.fflags & wanted) &&
        (wanted != NOTE_EXEC || !(ev.fflags & NOTE_EXIT));
}
static void child(int in, int out, int ignore) {
    // Also bounds orphan lifetime if the parent crashes. No raw-PID cleanup.
    alarm(8);
    reply_fd = out;
    signal(SIGTERM, ignore ? SIG_IGN : term);
    if (write(out, "R", 1) != 1) _exit(72);
    char c;
    while (read(in, &c, 1) == 1) {
        if (c == 'q') _exit(0);
        if (c == 'p' && write(out, "P", 1) != 1) _exit(73);
    }
    _exit(0);
}
static int run(int ignore, int (*send_token)(audit_token_t *, int)) {
    int input[2], output[2], kq = -1, ok = 0, status = 0;
    if (pipe(input) || pipe(output)) return 0;
    pid_t pid = fork();
    if (pid < 0) return 0;
    if (!pid) {
        close(input[1]); close(output[0]);
        child(input[0], output[1], ignore);
        _exit(74);
    }
    close(input[0]); close(output[1]);
    audit_token_t selected, mismatch;
    const char *stage = "ready";
    int mismatch_rc = -999, mismatch_errno = 0, signal_rc = -999, signal_errno = 0;
    int stale_rc = -999, stale_errno = 0;
    if (!byte(output[0], 'R')) goto cleanup;
    stage = "arm";
    if ((kq = arm(pid, &selected)) < 0) goto cleanup;
    mismatch = selected;
    mismatch.val[7] ^= 1; // Deliberately wrong native incarnation, same owned PID.
    stage = "mismatch-request";
    mismatch_rc = request(&mismatch, send_token, &mismatch_errno);
    // libproc returns the error number directly; errno is diagnostic only.
    if (mismatch_rc != ESRCH) goto cleanup;
    stage = "mismatch-responsive";
    if (!put(input[1], 'p') || !byte(output[0], 'P')) goto cleanup;
    stage = "pre-signal-no-exit";
    if (!event(kq, pid, 0, 50)) goto cleanup;
    stage = "correct-request";
    signal_rc = request(&selected, send_token, &signal_errno);
    if (signal_rc != 0) goto cleanup;
    if (ignore) {
        // Accepted request is not death; never escalate or authorize replacement.
        stage = "ignored-no-exit";
        if (!event(kq, pid, 0, 150)) goto cleanup;
        stage = "ignored-responsive";
        if (!put(input[1], 'p') || !byte(output[0], 'P')) goto cleanup;
        stage = "owned-quit";
        if (!put(input[1], 'q')) goto cleanup;
    } else {
        stage = "graceful-marker";
        if (!byte(output[0], 'T')) goto cleanup;
    }
    stage = "exit-event";
    if (!event(kq, pid, NOTE_EXIT, 1500)) goto cleanup;
    // Keep our exited child unreaped: its PID cannot be reused while testing
    // the stale-token refusal. Fixture isolation must not depend on that check.
    stage = "exited-unreaped-request";
    stale_rc = request(&selected, send_token, &stale_errno);
    if (stale_rc != ESRCH) goto cleanup;
    ok = 1;
cleanup:
    // Closing our pipe requests cooperative exit. Child alarm bounds wait even
    // on failed assertions; waitpid is only for our unreaped direct child.
    close(input[1]); close(output[0]);
    if (kq >= 0) close(kq);
    errno = 0;
    pid_t waited = waitpid(pid, &status, 0);
    int wait_errno = errno;
    if (waited != pid || !WIFEXITED(status) || WEXITSTATUS(status)) {
        if (ok) stage = "wait-child";
        ok = 0;
    }
    if (!ok) fprintf(stderr,
        "stage=%s mismatch_rc=%d mismatch_errno=%d signal_rc=%d signal_errno=%d stale_rc=%d stale_errno=%d "
        "acquire=%d info=%d count=%u release=%d event_rc=%d event_errno=%d flags=%u fflags=%u data=%ld "
        "queue_rc=%d queue_errno=%d write_rc=%d write_errno=%d poll_rc=%d poll_errno=%d read_rc=%d read_errno=%d byte=%d wait_matches=%d wait_errno=%d wait_status=%d\n",
        stage, mismatch_rc, mismatch_errno, signal_rc, signal_errno, stale_rc, stale_errno,
        diagnostic.acquire_rc, diagnostic.info_rc, diagnostic.info_count, diagnostic.release_rc,
        diagnostic.event_rc, diagnostic.event_errno, diagnostic.event_flags, diagnostic.event_fflags, diagnostic.event_data,
        diagnostic.queue_rc, diagnostic.queue_errno, diagnostic.write_rc, diagnostic.write_errno,
        diagnostic.poll_rc, diagnostic.poll_errno, diagnostic.read_rc, diagnostic.read_errno, diagnostic.observed_byte,
        waited == pid, wait_errno, status);
    printf("%s: %s\n", ignore ? "accepted-but-alive" : "graceful-exit", ok ? "PASS" : "FAIL");
    return ok;
}
// Exec is an application-binding invalidation, never a death certificate.
static int run_exec(const char *self) {
    int input[2], output[2], kq = -1, ok = 0, status = 0;
    if (pipe(input) || pipe(output)) return 0;
    pid_t pid = fork();
    if (pid < 0) return 0;
    if (!pid) {
        close(input[1]); close(output[0]); alarm(8);
        if (write(output[1], "R", 1) != 1) _exit(75);
        char c;
        if (read(input[0], &c, 1) != 1 || c != 'e') _exit(0);
        if (dup2(input[0], STDIN_FILENO) < 0 || dup2(output[1], STDOUT_FILENO) < 0) _exit(76);
        close(input[0]); close(output[1]);
        char *const args[] = {(char *)self, "--owned-exec-child", NULL};
        char *const env[] = {"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", NULL};
        execve(self, args, env);
        _exit(77);
    }
    close(input[0]); close(output[1]);
    audit_token_t selected;
    if (!byte(output[0], 'R') || (kq = arm(pid, &selected)) < 0) goto cleanup;
    if (write(input[1], "e", 1) != 1 || !byte(output[0], 'R')) goto cleanup;
    if (!event(kq, pid, NOTE_EXEC, 1500)) goto cleanup;
    if (write(input[1], "p", 1) != 1 || !byte(output[0], 'P')) goto cleanup;
    if (!event(kq, pid, 0, 50)) goto cleanup;
    // No signal to an exec-changed application. Owned pipe cleanup only.
    if (write(input[1], "q", 1) != 1 || !event(kq, pid, NOTE_EXIT, 1500)) goto cleanup;
    ok = 1;
cleanup:
    close(input[1]); close(output[0]);
    if (kq >= 0) close(kq);
    if (waitpid(pid, &status, 0) != pid || !WIFEXITED(status) || WEXITSTATUS(status)) ok = 0;
    printf("exec-is-not-exit: %s\n", ok ? "PASS" : "FAIL");
    return ok;
}
int main(int argc, char **argv) {
    // This internal mode has no signal or target lookup; only bounded pipe I/O.
    if (argc == 2 && !strcmp(argv[1], "--owned-exec-child")) {
        child(STDIN_FILENO, STDOUT_FILENO, 1);
        return 74;
    }
    if (argc != 1) return 64; // No external targets or paths accepted.
    signal(SIGPIPE, SIG_IGN);
    alarm(20);
    char path[PATH_MAX], self[PATH_MAX];
    uint32_t size = sizeof(path);
    if (_NSGetExecutablePath(path, &size) || !realpath(path, self)) return 78;
    int (*send_token)(audit_token_t *, int) = dlsym(RTLD_DEFAULT, "proc_signal_with_audittoken");
    if (!send_token) return 77;
    if (!run_exec(self) || !run(0, send_token)) return 1;
    return run(1, send_token) ? 0 : 1;
}
