#include "threads_darwin.h"

int
write_memory(mach_port_name_t task, mach_vm_address_t addr, void *d, mach_msg_type_number_t len) {
	kern_return_t kret;
	pointer_t data;
	memcpy((void *)&data, d, len);

	vm_region_submap_short_info_data_64_t info;
	mach_msg_type_number_t count = VM_REGION_SUBMAP_SHORT_INFO_COUNT_64;
	mach_vm_size_t l = len;
	mach_port_t objname;

	if (len == 1) l = 2;
	kret = mach_vm_region((vm_map_t)task, &(mach_vm_address_t){addr}, (mach_vm_size_t*)&l, VM_REGION_BASIC_INFO_64, (vm_region_info_t)&info, &count, &objname);
	if (kret != KERN_SUCCESS) return -1;

	// Set permissions to enable writting to this memory location
	kret = mach_vm_protect(task, addr, len, FALSE, VM_PROT_WRITE|VM_PROT_COPY|VM_PROT_READ);
	if (kret != KERN_SUCCESS) return -1;

	kret = mach_vm_write((vm_map_t)task, addr, (vm_offset_t)&data, len);
	if (kret != KERN_SUCCESS) return -1;

	// Restore virtual memory permissions
	kret = mach_vm_protect(task, addr, len, FALSE, info.protection);
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

kern_return_t
get_registers(mach_port_name_t task, x86_thread_state64_t *state) {
	kern_return_t kret;
	mach_msg_type_number_t stateCount = x86_THREAD_STATE64_COUNT;

	// TODO(dp) - possible memory leak - vm_deallocate state
	return thread_get_state(task, x86_THREAD_STATE64, (thread_state_t)state, &stateCount);
}

kern_return_t
set_pc(thread_act_t task, uint64_t pc) {
	kern_return_t kret;
	x86_thread_state64_t state;
	mach_msg_type_number_t stateCount = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(task, x86_THREAD_STATE64, (thread_state_t)&state, &stateCount);
	if (kret != KERN_SUCCESS) return kret;
	state.__rip = pc;

	return thread_set_state(task, x86_THREAD_STATE64, (thread_state_t)&state, stateCount);
}

kern_return_t
single_step(thread_act_t thread) {
	kern_return_t kret;
	x86_thread_state64_t regs;
	mach_msg_type_number_t count = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, &count);
	if (kret != KERN_SUCCESS) return kret;

	// Set trap bit in rflags
	regs.__rflags |= 0x100UL;

	kret = thread_set_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, count);
	if (kret != KERN_SUCCESS) return kret;

	kret = resume_thread(thread);
	if (kret != KERN_SUCCESS) return kret;

	return KERN_SUCCESS;
}

kern_return_t
resume_thread(thread_act_t thread) {
	kern_return_t kret;
	struct thread_basic_info info;
	unsigned int info_count = THREAD_BASIC_INFO_COUNT;

	kret = thread_info((thread_t)thread, THREAD_BASIC_INFO, (thread_info_t)&info, &info_count);
	if (kret != KERN_SUCCESS) return kret;

	for (int i = 0; i < info.suspend_count; i++) {
		kret = thread_resume(thread);
		if (kret != KERN_SUCCESS) break;
	}

	return KERN_SUCCESS;
}

kern_return_t
clear_trap_flag(thread_act_t thread) {
	kern_return_t kret;
	x86_thread_state64_t regs;
	mach_msg_type_number_t count = x86_THREAD_STATE64_COUNT;

	kret = thread_get_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, &count);
	if (kret != KERN_SUCCESS) return kret;

	// Clear trap bit in rflags
	regs.__rflags ^= 0x100UL;

	return thread_set_state(thread, x86_THREAD_STATE64, (thread_state_t)&regs, count);
}
