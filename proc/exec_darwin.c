#include "exec_darwin.h"

extern char** environ;

int
fork_exec(char *argv0, char **argv, int size,
		mach_port_name_t *task,
		mach_port_t *port_set,
		mach_port_t *exception_port,
		mach_port_t *notification_port)
{
	// In order to call PT_SIGEXC below, we must ensure that we have acquired the mach task first.
	// We facilitate this by creating a pipe and using it to let the forked process know that we've
	// finishing acquiring the mach task, and it can go ahead with the calls to PT_TRACE_ME and PT_SIGEXC.
	int fd[2];
	if (pipe(fd) < 0) return -1;

	kern_return_t kret;
	pid_t pid = fork();
	if (pid > 0) {
		// In parent.
		close(fd[0]);
		kret = acquire_mach_task(pid, task, port_set, exception_port, notification_port);
		if (kret != KERN_SUCCESS) return -1;

		char msg = 'c';
		write(fd[1], &msg, 1);
		close(fd[1]);
		return pid;
	}

	// Fork succeeded, we are in the child.
	int pret;
	char sig;

	close(fd[1]);
	read(fd[0], &sig, 1);
	close(fd[0]);

	// Create a new session for this process.
	if (setsid() < 0) {
		// Could not set session, but still want to create
		// a new process group.
		if (setpgid(0, 0) < 0) {
			return -1;
		}
	}

	// Set errno to zero before a call to ptrace.
	// It is documented that ptrace can return -1 even
	// for successful calls.
	errno = 0;
	pret = ptrace(PT_TRACE_ME, 0, 0, 0);
	if (pret != 0 && errno != 0) return -errno;

	errno = 0;
	pret = ptrace(PT_SIGEXC, 0, 0, 0);
	if (pret != 0 && errno != 0) return -errno;

	// Create the child process.
	execve(argv0, argv, environ);

	// We should never reach here, but if we did something went wrong.
	exit(1);
}
