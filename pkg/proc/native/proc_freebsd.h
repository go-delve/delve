#include <sys/types.h>
#include <sys/user.h>
#include <libutil.h>
#include <libprocstat.h>

char * find_command_name(int pid);
char * find_executable(int pid);
int find_status(int pid);
uintptr_t get_entry_point(int pid);
