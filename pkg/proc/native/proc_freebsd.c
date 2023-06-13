#include <sys/param.h>
#include <sys/mount.h>
#include <sys/queue.h>
#include <sys/sysctl.h>

#include <stdlib.h>
#include <string.h>
#include <errno.h>

#include "proc_freebsd.h"

/*
 * Returns the absolute pathname of the process's executable, if one was found.
 * Must be freed by the caller. Sets errno on failure.
 */
char * find_executable(int pid) {
	struct procstat *ps;
	struct kinfo_proc *kp;
	char *pathname;

	/*
	 * procstat_open_sysctl is for running processes. For core files, use
	 * procstat_open_core
	 */
	ps = procstat_open_sysctl();
	kp = kinfo_getproc(pid);
	pathname = malloc(MNAMELEN);
	if (ps && kp && pathname)
		procstat_getpathname(ps, kp, pathname, MNAMELEN);
	free(kp);
	procstat_close(ps);
	return (pathname);
}

/*
 * Returns the comm value of the process, which is usually the basename of its
 * executable. Must be freed by the caller.  Sets errno on failure.
 */
char * find_command_name(int pid) {
	char *command_name = NULL;
	struct kinfo_proc *kinfo;

	kinfo = kinfo_getproc(pid);
	if (kinfo != NULL) {
		command_name = malloc(COMMLEN + 1);
		if (command_name != NULL)
			strlcpy(command_name, kinfo->ki_comm, COMMLEN + 1);
		free(kinfo);
	}

	return (command_name);
}

int find_status(int pid){
	char status;
	struct kinfo_proc *kinfo;
	kinfo = kinfo_getproc(pid);
	if (kinfo != NULL)
		status = kinfo->ki_stat;
	else
		status = '?';
	free(kinfo);

	return (status);
}

uintptr_t get_entry_point(int pid) {
    void *ep = NULL;

    errno = EINVAL;

    struct procstat *ps = procstat_open_sysctl();
    if (ps == NULL)
        return 0;

    uint cnt = 0;
    struct kinfo_proc *kipp = procstat_getprocs(ps, KERN_PROC_PID, pid, &cnt);
    if (cnt == 0)
        return 0;

    Elf_Auxinfo *auxv = procstat_getauxv(ps, kipp, &cnt);
    if (auxv == NULL)
        return 0;

    for (int i = 0; i < cnt; i++) {
        if (auxv[i].a_type == AT_ENTRY) {
            ep = auxv[i].a_un.a_ptr;
            break;
        }
    }
    procstat_freeauxv(ps, auxv);
    errno = 0;
    return (uintptr_t)ep;
}
