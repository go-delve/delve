#include <sys/types.h>

char * find_command_name(int pid);
char * find_executable(int pid);
int find_status(int pid);
uintptr_t get_entry_point(int pid);
