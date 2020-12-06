//+build darwin,macnative

#include "exec_darwin.h"
#include "stdio.h"

extern char **environ;

#ifndef POSIX_SPAWN_DISABLE_ASLR
#define POSIX_SPAWN_DISABLE_ASLR 0x0100
#endif

int
spawn(char *argv0, char **argv, int size,
	  char *wd,
	  task_t *task,
	  mach_port_t *port_set,
	  mach_port_t *exception_port,
	  mach_port_t *notification_port,
	  int disable_aslr,
	  int fd_stdin,
	  int fd_stdout,
	  int fd_stderr) {
	kern_return_t kret = 0;

	posix_spawn_file_actions_t file_actions;
	posix_spawnattr_t attributes;

	// TODO: check error
	posix_spawnattr_init(&attributes);

	sigset_t no_signals;
	sigset_t all_signals;
	sigemptyset(&no_signals);
	sigfillset(&all_signals);

	posix_spawnattr_setsigmask(&attributes, &no_signals);
	posix_spawnattr_setsigdefault(&attributes, &all_signals);

	short flags = POSIX_SPAWN_START_SUSPENDED | POSIX_SPAWN_SETSIGDEF |
				  POSIX_SPAWN_SETSIGMASK;

	if (disable_aslr) {
		flags |= POSIX_SPAWN_DISABLE_ASLR;
	}

	posix_spawnattr_setflags(&attributes, flags);

	posix_spawn_file_actions_init(&file_actions);

	posix_spawn_file_actions_adddup2(&file_actions, fd_stdin, STDIN_FILENO); 
	posix_spawn_file_actions_adddup2(&file_actions, fd_stdout, STDOUT_FILENO); 
	posix_spawn_file_actions_adddup2(&file_actions, fd_stderr, STDERR_FILENO); 

	// Change working directory if wd is not empty.
	if (wd && wd[0]) {
		posix_spawn_file_actions_addchdir_np(&file_actions, wd);
	}

	pid_t pid;

	posix_spawnp(&pid, argv0, &file_actions, &attributes,
				 argv,
				 environ);

	kret = acquire_mach_task(pid, task, port_set, exception_port, notification_port);
	if (kret != KERN_SUCCESS) {
		return -1;
	}

	int err = ptrace(PT_ATTACHEXC, pid, 0, 0);
	if (err != 0) {
		perror("ptrace");
		return -1;
	}

	posix_spawnattr_destroy(&attributes);

	return pid;
}
