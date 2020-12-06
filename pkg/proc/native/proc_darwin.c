//+build darwin,macnative

#include "proc_darwin.h"

static const unsigned char info_plist[]
	__attribute__((section("__TEXT,__info_plist"), used)) =
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
		"<!DOCTYPE plist PUBLIC \"-//Apple Computer//DTD PLIST 1.0//EN\""
		" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"
		"<plist version=\"1.0\">\n"
		"<dict>\n"
		"  <key>CFBundleIdentifier</key>\n"
		"  <string>org.dlv</string>\n"
		"  <key>CFBundleName</key>\n"
		"  <string>delve</string>\n"
		"  <key>CFBundleVersion</key>\n"
		"  <string>1.0</string>\n"
		"  <key>SecTaskAccess</key>\n"
		"  <array>\n"
		"    <string>allowed</string>\n"
		"    <string>debug</string>\n"
		"  </array>\n"
		"</dict>\n"
		"</plist>\n";

kern_return_t
acquire_mach_task(int tid,
				  task_t *task,
				  mach_port_t *port_set,
				  mach_port_t *exception_port,
				  mach_port_t *notification_port) {
	kern_return_t kret;
	mach_port_t prev_not;
	mach_port_t self = mach_task_self();

	kret = task_for_pid(self, tid, task);
	if (kret != KERN_SUCCESS) return kret;

	// Allocate exception port.
	kret = mach_port_allocate(self, MACH_PORT_RIGHT_RECEIVE, exception_port);
	if (kret != KERN_SUCCESS) return kret;

	kret = mach_port_insert_right(self, *exception_port, *exception_port, MACH_MSG_TYPE_MAKE_SEND);
	if (kret != KERN_SUCCESS) return kret;

	kret = task_set_exception_ports(*task, EXC_MASK_BREAKPOINT | EXC_MASK_SOFTWARE, *exception_port,
									EXCEPTION_DEFAULT | MACH_EXCEPTION_CODES, THREAD_STATE_NONE);
	if (kret != KERN_SUCCESS) return kret;

	// Allocate notification port to alert of when the process dies.
	kret = mach_port_allocate(self, MACH_PORT_RIGHT_RECEIVE, notification_port);
	if (kret != KERN_SUCCESS) return kret;

	kret = mach_port_insert_right(self, *notification_port, *notification_port, MACH_MSG_TYPE_MAKE_SEND);
	if (kret != KERN_SUCCESS) return kret;

	kret = mach_port_request_notification(self, *task, MACH_NOTIFY_DEAD_NAME, 0, *notification_port,
										  MACH_MSG_TYPE_MAKE_SEND_ONCE, &prev_not);
	if (kret != KERN_SUCCESS) return kret;

	// Create port set.
	kret = mach_port_allocate(self, MACH_PORT_RIGHT_PORT_SET, port_set);
	if (kret != KERN_SUCCESS) return kret;

	// Move exception and notification ports to port set.
	kret = mach_port_move_member(self, *exception_port, *port_set);
	if (kret != KERN_SUCCESS) return kret;

	return mach_port_move_member(self, *notification_port, *port_set);
}

kern_return_t
reset_exception_ports(task_t task, mach_port_t *exception_port, mach_port_t *notification_port) {
	kern_return_t kret;
	mach_port_t prev_not;
	mach_port_t self = mach_task_self();

	kret = task_set_exception_ports(task, EXC_MASK_BREAKPOINT | EXC_MASK_SOFTWARE, *exception_port,
									EXCEPTION_DEFAULT, THREAD_STATE_NONE);
	if (kret != KERN_SUCCESS) return kret;

	kret = mach_port_request_notification(self, task, MACH_NOTIFY_DEAD_NAME, 0, *notification_port,
										  MACH_MSG_TYPE_MAKE_SEND_ONCE, &prev_not);
	if (kret != KERN_SUCCESS) return kret;

	return KERN_SUCCESS;
}

mach_vm_address_t
get_macho_header_offset(task_t task) {
	kern_return_t kret;
	mach_vm_address_t address;
	mach_vm_size_t size;
	vm_region_basic_info_data_64_t info;
	mach_msg_type_number_t infoCnt = VM_REGION_BASIC_INFO_COUNT_64;
	mach_port_t object_name;

	// start at 0x0
	address = 0x0;

	kret = mach_vm_region(task, &address, &size, VM_REGION_BASIC_INFO_64,
										(vm_region_info_64_t)&info, &infoCnt, &object_name);
	// Return 0 if we cannot find the address, since we implicitly use this to denote
	// that we do not have the entrypoint in bininfo
	if (kret != KERN_SUCCESS) return 0;

	// address will then contain the start address of the section after __PAGEZERO, i.e. the mach-o header
	return address;
}

char *
find_executable(int pid) {
	static char pathbuf[PATH_MAX];
	proc_pidpath(pid, pathbuf, PATH_MAX);
	return pathbuf;
}

kern_return_t
get_threads(task_t task, void *slice, int limit) {
	kern_return_t kret;
	thread_act_array_t list;
	mach_msg_type_number_t count;

	kret = task_threads(task, &list, &count);
	if (kret != KERN_SUCCESS) {
		return kret;
	}

	if (count > limit) {
		vm_deallocate(mach_task_self(), (vm_address_t)list, count * sizeof(list[0]));
		return -2;
	}

	memcpy(slice, (void *)list, count * sizeof(list[0]));

	kret = vm_deallocate(mach_task_self(), (vm_address_t)list, count * sizeof(list[0]));
	if (kret != KERN_SUCCESS) return kret;

	return (kern_return_t)0;
}

int
thread_count(task_t task) {
	kern_return_t kret;
	thread_act_array_t list;
	mach_msg_type_number_t count;

	kret = task_threads(task, &list, &count);
	if (kret != KERN_SUCCESS) return -1;

	kret = vm_deallocate(mach_task_self(), (vm_address_t)list, count * sizeof(list[0]));
	if (kret != KERN_SUCCESS) return -1;

	return count;
}

const char *
exception_string(int code) {
	switch (code) {
		case EXC_SOFTWARE:
			return "EXC_SOFTWARE";
		case EXC_BREAKPOINT:
			return "EXC_BREAKPOINT";
		case EXC_BAD_ACCESS:
			return "EXC_BAD_ACCESS";
	}

	return NULL;
}

const char *
signal_string(int signal) {
	switch (signal) {
		case SIGHUP:
			return "SIGHUP";
		case SIGKILL:
			return "SIGKILL";
		case SIGSEGV:
			return "SIGSEGV";
		case SIGSTOP:
			return "SIGSTOP";
		case SIGURG:
			return "SIGHUP";
		default:
			return NULL;
	}

	return NULL;
}

mach_port_t
mach_port_wait(mach_port_t port_set, task_t *task, int nonblocking) {
	kern_return_t kret;
	mach_msg_option_t opts = MACH_RCV_MSG | MACH_RCV_INTERRUPT;
	if (nonblocking) {
		opts |= MACH_RCV_TIMEOUT;
	}

	char req[128];

loop:
	// Wait for mach msg.
	kret = mach_msg((mach_msg_header_t *)&req, opts,
					0, sizeof(req), port_set, 10, MACH_PORT_NULL);
	if (kret == MACH_RCV_INTERRUPTED) return kret;
	if (kret != MACH_MSG_SUCCESS) return 0;

	__Request__mach_exception_raise_t *msg = (__Request__mach_exception_raise_t *)req;

	switch (msg->Head.msgh_id) {
		case 2405: { // Exception
			// 2405 is the mach_exception_raise event, defined in:
			// /Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/include/mach/mach_exc.defs
			// compile this file with mig to get the C version of the description

			exception_type_t exception_type = msg->exception;
			mach_msg_type_number_t code_count = msg->codeCnt;

			// Propagate task back to Go
			*task = msg->task.name;

			// printf("Caught exception; exception_type (0x%02x): %s, code_count: 0x%02x, msg->code[0]: 0x%016llx, msg->code[1]: 0x%016llx\n", exception_type, exception_string(exception_type), code_count, msg->code[0], msg->code[1]);

			if (thread_suspend(msg->thread.name) != KERN_SUCCESS) return 0;

			if (exception_type == EXC_SOFTWARE && msg->code[0] == EXC_SOFT_SIGNAL) {
				pid_t pid;
				pid_for_task(msg->task.name, &pid);

				// printf("Retrieved UNIX signal %s for 0x%d...\n", signal_string(msg->code[1]), msg->thread.name);

				int signal = msg->code[1];

				// Try to relay the signal to the inferior
				errno = 0;
				int err = ptrace(PT_THUPDATE, pid, (caddr_t)((uintptr_t)msg->thread.name), signal);
				if (err != 0 && errno != 0) {
					// Warn the user, but try to continue, to at least send the reply back to the kernel
					fprintf(stderr, "Could not clear signal %s for thread 0x%d: %s\n", signal_string(msg->code[1]), msg->thread.name, strerror(errno));
				}
			}

			// Send our reply back so the kernel knows this exception has been handled.
			kret = mach_send_reply(msg->Head);
			if (kret != MACH_MSG_SUCCESS) return 0;

			// Stop at SIGSTOP, i.e. so we can intercept it at Launch
			if (exception_type == EXC_SOFTWARE && msg->code[0] == EXC_SOFT_SIGNAL && msg->code[1] == SIGSTOP) {
				return msg->thread.name;
			}

#if defined(__arm64__)
			if (exception_type == EXC_BREAKPOINT /*&& msg->code[0] == EXC_ARM_BREAKPOINT*/) {
				// Yay, found a breakpoint
				return msg->thread.name;
			}
#else
			if (exception_type == EXC_BREAKPOINT /*&& (msg->code[0] == EXC_I386_SGL || msg->code[0] == EXC_I386_BPT)*/) {
				// Yay, found a breakpoint
				return msg->thread.name;
			}
#endif
			// Otherwise, resume the thread
			if (thread_resume(msg->thread.name) != KERN_SUCCESS) return 0;

			// And wait again
			goto loop;
		}

		case 72: { // Death
			// printf("Caught mach_notify_dead_name\n");

			// 72 is mach_notify_dead_name, defined in:
			// /Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/include/mach/notify.defs
			// compile this file with mig to get the C version of the description

			// Return the notification port, so we know it is a notification.
			// This is a bit confusing, but the easiest way to distinguish between
			// notifications and exceptions
			return msg->Head.msgh_local_port;
		}
	}

	return 0;
}

kern_return_t
mach_send_reply(mach_msg_header_t hdr) {
	mig_reply_error_t reply;
	mach_msg_header_t *rh = &reply.Head;
	rh->msgh_bits = MACH_MSGH_BITS(MACH_MSGH_BITS_REMOTE(hdr.msgh_bits), 0);
	rh->msgh_remote_port = hdr.msgh_remote_port;
	rh->msgh_size = (mach_msg_size_t)sizeof(mig_reply_error_t);
	rh->msgh_local_port = MACH_PORT_NULL;
	rh->msgh_id = hdr.msgh_id + 100;

	reply.NDR = NDR_record;
	reply.RetCode = KERN_SUCCESS;

	return mach_msg(&reply.Head, MACH_SEND_MSG | MACH_SEND_INTERRUPT, rh->msgh_size, 0,
					MACH_PORT_NULL, MACH_MSG_TIMEOUT_NONE, MACH_PORT_NULL);
}

kern_return_t
raise_exception(mach_port_t task, mach_port_t thread, mach_port_t exception_port, exception_type_t exception) {
	return mach_exception_raise(exception_port, thread, task, exception, 0, 0);
}

task_t
get_task_for_pid(int pid) {
	task_t task = 0;
	mach_port_t self = mach_task_self();

	task_for_pid(self, pid, &task);
	return task;
}

int
task_is_valid(task_t task) {
	struct task_basic_info info;
	mach_msg_type_number_t count = TASK_BASIC_INFO_COUNT;
	return task_info(task, TASK_BASIC_INFO, (task_info_t)&info, &count) == KERN_SUCCESS;
}
