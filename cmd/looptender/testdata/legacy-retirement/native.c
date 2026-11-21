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
static void term(int sig) {
    (void)sig;
    if (write(reply_fd, "T", 1) != 1) _exit(71);
    _exit(0);
}
static int byte(int fd, char expected) {
    struct pollfd p = {fd, POLLIN, 0};
    char b = 0;
    return poll(&p, 1, 1500) == 1 && read(fd, &b, 1) == 1 && b == expected;
}
static int token(pid_t pid, audit_token_t *out) {
    mach_port_t port = MACH_PORT_NULL;
    if (task_name_for_pid(mach_task_self(), pid, &port) != KERN_SUCCESS) return 0;
    mach_msg_type_number_t n = TASK_AUDIT_TOKEN_COUNT;
    kern_return_t kr = task_info(port, TASK_AUDIT_TOKEN, (task_info_t)out, &n);
    kern_return_t released = mach_port_deallocate(mach_task_self(), port);
    return kr == KERN_SUCCESS && released == KERN_SUCCESS && n == TASK_AUDIT_TOKEN_COUNT &&
        audit_token_to_pid(*out) == pid && audit_token_to_euid(*out) == geteuid();
}
// Receipt proves registration succeeded. Fresh acquisition after registration
// must match the pre-registration incarnation. No late registration after signal.
static int arm(pid_t pid, audit_token_t *bound) {
    audit_token_t after;
    if (!token(pid, bound)) return -1;
    int kq = kqueue();
    if (kq < 0) return -1;
    struct kevent change, receipt;
    EV_SET(&change, (uintptr_t)pid, EVFILT_PROC, EV_ADD | EV_RECEIPT,
        NOTE_EXIT | NOTE_EXEC, 0, NULL);
    struct timespec zero = {0, 0};
    if (kevent(kq, &change, 1, &receipt, 1, &zero) != 1 ||
        !(receipt.flags & EV_ERROR) || receipt.data != 0 ||
        !token(pid, &after) || memcmp(bound, &after, sizeof(after))) {
        close(kq); return -1;
    }
    return kq;
}
static int event(int kq, pid_t pid, unsigned wanted, int milliseconds) {
    struct kevent ev;
    struct timespec limit = {milliseconds / 1000, (milliseconds % 1000) * 1000000L};
    int n = kevent(kq, NULL, 0, &ev, 1, &limit);
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
    if (!byte(output[0], 'R') || (kq = arm(pid, &selected)) < 0) goto cleanup;
    mismatch = selected;
    mismatch.val[7] ^= 1; // Deliberately wrong native incarnation, same owned PID.
    errno = 0;
    if (send_token(&mismatch, SIGTERM) != -1 || errno != ESRCH) goto cleanup;
    if (write(input[1], "p", 1) != 1 || !byte(output[0], 'P')) goto cleanup;
    if (!event(kq, pid, 0, 50)) goto cleanup;
    if (send_token(&selected, SIGTERM) != 0) goto cleanup;
    if (ignore) {
        // Accepted request is not death; never escalate or authorize replacement.
        if (!event(kq, pid, 0, 150)) goto cleanup;
        if (write(input[1], "p", 1) != 1 || !byte(output[0], 'P')) goto cleanup;
        if (write(input[1], "q", 1) != 1) goto cleanup;
    } else if (!byte(output[0], 'T')) goto cleanup;
    if (!event(kq, pid, NOTE_EXIT, 1500)) goto cleanup;
    ok = 1;
cleanup:
    // Closing our pipe requests cooperative exit. Child alarm bounds wait even
    // on failed assertions; waitpid is only for our unreaped direct child.
    close(input[1]); close(output[0]);
    if (kq >= 0) close(kq);
    if (waitpid(pid, &status, 0) != pid || !WIFEXITED(status) || WEXITSTATUS(status)) ok = 0;
    if (ok) {
        errno = 0;
        if (send_token(&selected, SIGTERM) != -1 || errno != ESRCH) ok = 0;
    }
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
