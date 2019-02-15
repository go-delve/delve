#include <stddef.h>

struct ptrace_lwpinfo;

unsigned char* ptrace_get_xsave(int tid, size_t *len);
int ptrace_get_lwp_list(int pid, int *tids, size_t len);
int ptrace_get_num_lwps(int pid);
int ptrace_lwp_info(int tid, struct ptrace_lwpinfo *info);
int ptrace_read(int tid, uintptr_t addr, void *buf, ssize_t len);
int ptrace_write(int tid, uintptr_t addr, void *buf, ssize_t len);
