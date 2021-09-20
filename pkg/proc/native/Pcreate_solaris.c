#include <libproc.h>

char *Pcreate_dir = NULL;
int Pcreate_stdin = STDIN_FILENO;
int Pcreate_stdout = STDOUT_FILENO;
int Pcreate_stderr = STDERR_FILENO;

// Pcreate_callback is called by libproc when Pcreate is called,
// after forking and prior to execing the victim process.
void
Pcreate_callback(struct ps_prochandle *P)
{
	if (Pcreate_dir != NULL) {
		(void) chdir(Pcreate_dir);
	}
	if (Pcreate_stdin != STDIN_FILENO) {
		(void) dup2(Pcreate_stdin, STDIN_FILENO);
	}
	if (Pcreate_stdout != STDOUT_FILENO) {
		(void) dup2(Pcreate_stdout, STDOUT_FILENO);
	}
	if (Pcreate_stderr != STDERR_FILENO) {
		(void) dup2(Pcreate_stderr, STDERR_FILENO);
	}
}
