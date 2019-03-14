#include <stddef.h>

struct ptrace_lwpinfo;

unsigned char* ptrace_get_xsave(int tid, size_t *len);
int ptrace_get_lwp_list(int pid, int *tids, size_t len);
int ptrace_get_num_lwps(int pid);
