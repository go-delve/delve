#include <stdlib.h>
#include <stdio.h>
#include <errno.h>
#include <libproc.h>
#include <sys/ptrace.h>
#include <unistd.h>

extern char** environ;

int main(int argc, char *argv[]) {
	int pret;

	fflush(stdout);
	errno = 0;
	pret = ptrace(PT_TRACE_ME, 0, 0, 0);
	if (pret != 0 && errno != 0) {
		exit(EXIT_FAILURE);
	}

	errno = 0;
	pret = ptrace(PT_SIGEXC, 0, 0, 0);
	if (pret != 0 && errno != 0) {
		exit(EXIT_FAILURE);
	}

	while (sleep(2) != 0);

	execve(argv[1], &argv[1], environ);
}
