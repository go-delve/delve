#include "threads_darwin.h"

int
write_memory(mach_port_name_t task, mach_vm_address_t addr, void *d, mach_msg_type_number_t len) {
	kern_return_t kret;
	pointer_t data;
	memcpy((void *)&data, d, len);

	// Set permissions to enable writting to this memory location
	kret = mach_vm_protect(task, addr, len, FALSE, VM_PROT_READ | VM_PROT_WRITE | VM_PROT_COPY);
	if (kret != KERN_SUCCESS) return -1;

	kret = mach_vm_write((vm_map_t)task, addr, (vm_offset_t)&data, len);
	if (kret != KERN_SUCCESS) return -1;

	// Restore virtual memory permissions
	// TODO(dp) this should take into account original permissions somehow
	kret = mach_vm_protect(task, addr, len, FALSE, VM_PROT_READ | VM_PROT_EXECUTE);
	if (kret != KERN_SUCCESS) return -1;

	return 0;
}

int
read_memory(mach_port_name_t task, mach_vm_address_t addr, void *d, mach_msg_type_number_t len) {
	kern_return_t kret;
	pointer_t data;
	mach_msg_type_number_t count;

	kret = mach_vm_read((vm_map_t)task, addr, len, &data, &count);
	if (kret != KERN_SUCCESS) return -1;
	memcpy(d, (void *)data, len);

	kret = vm_deallocate(task, data, len);
	if (kret != KERN_SUCCESS) return -1;

	return count;
}

x86_thread_state64_t
get_registers(mach_port_name_t task) {
	kern_return_t kret;
	x86_thread_state64_t state;
	mach_msg_type_number_t stateCount = x86_THREAD_STATE64_COUNT;

	// TODO(dp) - possible memory leak - vm_deallocate state
	kret = thread_get_state(task, x86_THREAD_STATE64, (thread_state_t)&state, &stateCount);
	if (kret != KERN_SUCCESS) printf("SOMETHING WENT WRONG-------------- %d\n", kret);
	if (kret == KERN_INVALID_ARGUMENT) puts("INAVLID ARGUMENT");

	return state;
}

// TODO(dp) this should return kret instead of void
void
set_pc(thread_act_t task, uint64_t pc) {
	kern_return_t kret;
	x86_thread_state64_t state;
	mach_msg_type_number_t stateCount = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(task, x86_THREAD_STATE64, (thread_state_t)&state, &stateCount);
	if (kret != KERN_SUCCESS) puts(mach_error_string(kret));
	state.__rip = pc;

	kret = thread_set_state(task, x86_THREAD_STATE64, (thread_state_t)&state, stateCount);
	if (kret != KERN_SUCCESS) puts(mach_error_string(kret));
	// TODO(dp) - possible memory leak - vm_deallocate state
}

// TODO(dp) this should return kret instead of void
void
single_step(thread_act_t thread) {
	kern_return_t kret;
	x86_thread_state64_t regs;
	mach_msg_type_number_t count = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, &count);
	if (kret != KERN_SUCCESS) {
		puts("get state fail");
		puts(mach_error_string(kret));
	}
	// Set trap bit in rflags
	regs.__rflags |= 0x100UL;

	kret = thread_set_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, count);
	if (kret != KERN_SUCCESS) {
		puts("set state fail");
		puts(mach_error_string(kret));
	}
	// TODO(dp) vm deallocate state?

	// Continue here until we've fully decremented suspend_count
	for (;;) {
		kret = thread_resume(thread);
		if (kret != KERN_SUCCESS) break;
	}
}

// TODO(dp) return kret
void
clear_trap_flag(thread_act_t thread) {
	kern_return_t kret;
	x86_thread_state64_t regs;
	mach_msg_type_number_t count = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, &count);
	if (kret != KERN_SUCCESS) {
		puts("get state fail");
		puts(mach_error_string(kret));
	}
	// Clear trap bit in rflags
	regs.__rflags ^= 0x100UL;

	kret = thread_set_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, count);
	if (kret != KERN_SUCCESS) {
		puts("set state fail");
		puts(mach_error_string(kret));
	}
	// TODO(dp) vm deallocate state?
}
