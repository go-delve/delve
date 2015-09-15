#include "proc_darwin.h"

#include <unistd.h>
#include <sys/ptrace.h>
#include <errno.h>
#include <stdlib.h>

int
fork_exec(char *, char **, int, mach_port_name_t*, mach_port_t*, mach_port_t*, mach_port_t*);
