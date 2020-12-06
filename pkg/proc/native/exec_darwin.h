//+build darwin,macnative

#include "proc_darwin.h"

#include <unistd.h>
#include <sys/ptrace.h>
#include <errno.h>
#include <stdlib.h>
#include <fcntl.h>
#include <spawn.h>
#include <signal.h>

int
spawn(char *, char **, int, char *, task_t*, mach_port_t*, mach_port_t*, mach_port_t*, int, int, int, int);
