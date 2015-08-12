#include <sys/types.h>
#include <libproc.h>
#include <mach/mach.h>
#include <mach/mach_vm.h>
#include "mach_exc.h"
#include "exc.h"

#ifdef	mig_external
mig_external
#else
extern
#endif	/* mig_external */
boolean_t exc_server(
		mach_msg_header_t *InHeadP,
		mach_msg_header_t *OutHeadP);

#ifdef	mig_external
mig_external
#else
extern
#endif	/* mig_external */
boolean_t mach_exc_server(
		mach_msg_header_t *InHeadP,
		mach_msg_header_t *OutHeadP);

kern_return_t
acquire_mach_task(int, mach_port_name_t*, mach_port_t*, mach_port_t*, mach_port_t*);

char *
find_executable(int pid);

kern_return_t
get_threads(task_t task, void *);

int
thread_count(task_t task);

mach_port_t
mach_port_wait(mach_port_t);

kern_return_t
mach_send_reply(mach_msg_header_t);

kern_return_t
raise_exception(mach_port_t, mach_port_t, mach_port_t, exception_type_t);

