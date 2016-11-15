#include <stdlib.h>
#include <sys/types.h>
#include <mach/mach.h>
#include <mach/mach_vm.h>
#include <mach/thread_info.h>

int
write_memory(task_t, mach_vm_address_t, void *, mach_msg_type_number_t);

int
read_memory(task_t, mach_vm_address_t, void *, mach_msg_type_number_t);

kern_return_t
get_registers(mach_port_name_t, x86_thread_state64_t*);

kern_return_t
get_fpu_registers(mach_port_name_t, x86_float_state64_t *);

kern_return_t
set_pc(thread_act_t, uint64_t);

kern_return_t
single_step(thread_act_t);

kern_return_t
clear_trap_flag(thread_act_t);

kern_return_t
resume_thread(thread_act_t);

kern_return_t
set_registers(mach_port_name_t, x86_thread_state64_t*);

kern_return_t
get_identity(mach_port_name_t, thread_identifier_info_data_t *);

int
thread_blocked(thread_act_t thread);

int
num_running_threads(task_t task);
