#include "proctl_darwin.h"


static const unsigned char info_plist[]
__attribute__ ((section ("__TEXT,__info_plist"),used)) =
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

static thread_act_t _global_thread;

kern_return_t
acquire_mach_task(int tid, mach_port_name_t *task, mach_port_t *exception_port) {
  kern_return_t kret;
  mach_port_t prev_not;
  mach_port_t self = mach_task_self();

  kret = task_for_pid(self, tid, task);
  if (kret != KERN_SUCCESS) return kret;

  kret = mach_port_allocate(self, MACH_PORT_RIGHT_RECEIVE, exception_port);
  if (kret != KERN_SUCCESS) return kret;

  kret = mach_port_insert_right(self, *exception_port, *exception_port, MACH_MSG_TYPE_MAKE_SEND);
  if (kret != KERN_SUCCESS) return kret;

  kret = mach_port_request_notification(self, *task, MACH_NOTIFY_DEAD_NAME, 0, *exception_port, MACH_MSG_TYPE_MAKE_SEND_ONCE,
           &prev_not);
  if (kret != KERN_SUCCESS) return kret;

  // Set exception port
  return task_set_exception_ports(*task, EXC_MASK_BREAKPOINT|EXC_MASK_SOFTWARE, *exception_port,
    EXCEPTION_DEFAULT, THREAD_STATE_NONE);
}

char *
find_executable(int pid) {
	static char pathbuf[PATH_MAX];
	proc_pidpath(pid, pathbuf, PATH_MAX);
	return pathbuf;
}

kern_return_t
get_threads(task_t task, void *slice) {
  kern_return_t kret;
  thread_act_array_t list;
  mach_msg_type_number_t count;

  kret = task_threads(task, &list, &count);
  if (kret != KERN_SUCCESS) {
    return kret;
  }

  memcpy(slice, (void*)list, count*sizeof(list[0]));

  kret = vm_deallocate(mach_task_self(), (vm_address_t) list, count * sizeof(list[0]));
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

  kret = vm_deallocate(mach_task_self(), (vm_address_t) list, count * sizeof(list[0]));
  if (kret != KERN_SUCCESS) return -1;

  return count;
}

typedef struct exc_msg {
  mach_msg_header_t Head;
  mach_msg_body_t msgh_body;
  mach_msg_port_descriptor_t thread;
  mach_msg_port_descriptor_t task;
  NDR_record_t NDR;
  exception_type_t exception;
  mach_msg_type_number_t codeCnt;
  exception_data_t code;
  char pad[512];
} exc_msg_t;

thread_act_t
mach_port_wait(mach_port_t port) {
  mach_msg_return_t msg = mach_msg_server_once(exc_server, sizeof(exc_msg_t), port, MACH_MSG_TIMEOUT_NONE);
  if (msg != MACH_MSG_SUCCESS) {
    return -1;
  }
  return _global_thread;
}

// 64 bit exception handlers

kern_return_t
catch_mach_exception_raise(
  mach_port_t exception_port,
  mach_port_t thread,
  mach_port_t task,
  exception_type_t exception,
  mach_exception_data_t code,
  mach_msg_type_number_t codeCnt)
{
  _global_thread = (thread_act_t)thread;
  thread_suspend(thread);
  return KERN_SUCCESS;
}

kern_return_t
catch_mach_exception_raise_state(
  mach_port_t exception_port,
  exception_type_t exception,
  const mach_exception_data_t code,
  mach_msg_type_number_t codeCnt,
  int *flavor,
  const thread_state_t old_state,
  mach_msg_type_number_t old_stateCnt,
  thread_state_t new_state,
  mach_msg_type_number_t *new_stateCnt)
{
  return KERN_SUCCESS;
}

kern_return_t
catch_mach_exception_raise_state_identity(
  mach_port_t exception_port,
  mach_port_t thread,
  mach_port_t task,
  exception_type_t exception,
  mach_exception_data_t code,
  mach_msg_type_number_t codeCnt,
  int *flavor,
  thread_state_t old_state,
  mach_msg_type_number_t old_stateCnt,
  thread_state_t new_state,
  mach_msg_type_number_t *new_stateCnt)
{
  return KERN_SUCCESS;
}

// 32 bit exception handlers

kern_return_t
catch_exception_raise(
  mach_port_t exception_port,
  mach_port_t thread,
  mach_port_t task,
  exception_type_t exception,
  mach_exception_data_t code,
  mach_msg_type_number_t codeCnt)
{
  _global_thread = (thread_act_t)thread;
  thread_suspend(thread);
  return KERN_SUCCESS;
}

kern_return_t
catch_exception_raise_state(
  mach_port_t exception_port,
  exception_type_t exception,
  const mach_exception_data_t code,
  mach_msg_type_number_t codeCnt,
  int *flavor,
  const thread_state_t old_state,
  mach_msg_type_number_t old_stateCnt,
  thread_state_t new_state,
  mach_msg_type_number_t *new_stateCnt)
{
  return KERN_SUCCESS;
}

kern_return_t
catch_exception_raise_state_identity(
  mach_port_t exception_port,
  mach_port_t thread,
  mach_port_t task,
  exception_type_t exception,
  mach_exception_data_t code,
  mach_msg_type_number_t codeCnt,
  int *flavor,
  thread_state_t old_state,
  mach_msg_type_number_t old_stateCnt,
  thread_state_t new_state,
  mach_msg_type_number_t *new_stateCnt)
{
  return KERN_SUCCESS;
}
