#include <sys/types.h>
#include <sys/ptrace.h>

#include <errno.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#include "ptrace_freebsd_amd64.h"

/* Returns the number of kernel threads associated with the traced process. */
int ptrace_get_num_lwps(int pid) {
	int ret;
	errno = 0;
	ret = ptrace(PT_GETNUMLWPS, (pid_t)pid, 0, 0);
	return (ret);
}

/*
 * Fetches the list of LWPs for a given process into tids.  Returns the number
 * of LWP entries filled in. Sets errno on return.
 */
int ptrace_get_lwp_list(int pid, int *tids, size_t len) {
	int ret;
	errno = 0;
	ret = ptrace(PT_GETLWPLIST, (pid_t)pid, (caddr_t)tids, len);
	return (ret);
}

/*
 * Returns a pointer to the X86 XSAVE data, or NULL on failure.  Returns the
 * length of the buffer in the len argument.  Must be freed when no longer in
 * use.  Modifies errno.
 */
unsigned char* ptrace_get_xsave(int tid, size_t *len) {
	static ssize_t xsave_len = 0;
	static int getxstate_info_errno = 0;
	unsigned char *buf;
	int err;

	if (xsave_len == 0) {
		/* Haven't tried to set the size yet */
		struct ptrace_xstate_info info;
		err = ptrace(PT_GETXSTATE_INFO, (pid_t)tid,
			     (caddr_t)&info, sizeof(info));
		if (err == 0)
			xsave_len = info.xsave_len;
		else {
			xsave_len = -1;
			getxstate_info_errno = errno;
		}
	}
	if (xsave_len < 0) {
		/* Not supported on this system */
		errno = getxstate_info_errno;
		return (NULL);
	}

	buf = malloc(xsave_len);
	if (buf == NULL) {
		errno;
		return (NULL);
	}
	err = ptrace(PT_GETXSTATE, (pid_t)tid, (caddr_t)buf, xsave_len);
	if (err == 0) {
		errno = 0;
		*len = xsave_len;
		return (buf);
	} else {
		free(buf);
		return (NULL);
	}
}

int ptrace_lwp_info(int tid, struct ptrace_lwpinfo *info) {
	return (ptrace(PT_LWPINFO, tid, (caddr_t)info, sizeof(*info)));
}

int ptrace_read(int tid, uintptr_t addr, void *buf, ssize_t len) {
    struct ptrace_io_desc ioreq = {PIOD_READ_I, (void *)addr, buf, len};
    errno = 0;
    return ptrace(PT_IO, tid, (caddr_t)&ioreq, 0);
}

int ptrace_write(int tid, uintptr_t addr, void *buf, ssize_t len) {
    struct ptrace_io_desc ioreq = {PIOD_WRITE_I, (void *)addr, buf, len};
    errno = 0;
    return ptrace(PT_IO, tid, (caddr_t)&ioreq, 0);
}
