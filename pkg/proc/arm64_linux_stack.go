package proc

import (
	"unsafe"
)

// The following code is specific to Linux and and the ARM64.  It sees if the
// current return address points to the return-from-exception system call.
// If so, it will locate the previous stack frame and it will set the CFA to
// point to this previous stack frame.  It seems likely that code similar to
// this will be useful for other architectures.  
//
// This code was adapted from "gdb".
//
// It might be possible to eliminate the offsets below by referring to
// Dwarf symbols and/or exception routine name lookups.  For now, this will
// have to wait until ARM64 Delve can more reliably look up local stack
// variables.  Another (less satisfactory) way would be to duplicate Linux's
// runtime exception structure's layouts, which is what gdb does.
//
// It might be good to put a "tag" into the stack structure indicating
// when a frame is an "exception" frame, and hence "jumps".  Further,
// exception frames contain the registers at the time of the exception.
// When a GO program is compiled without debugging symbols, this register
// structure should be used to fetch saved registers, thus allowing Delve
// to print variables from previous frames that were still in registers
// at the time of the procedure calls.

func checkException_linux_arm64(it *stackIterator, ret *uint64) {
	const sigreturn0 = 0xd2801168	// movz x8, 0x8b    (return from
	const sigreturn4 = 0xd4000001	// svc  0x0          exception)

	const sigreturnoffset = -0x18	// offset to signal return code fragment
	const returnoffset    = 0x1a0	// offset to signal's return addresses

	inst_size := int64(uintptr(unsafe.Sizeof(uint32(0))))

	// See if the current return is to a return-from-execption system call.

	val, err := readUintRaw(it.mem, uintptr(*ret), inst_size)
	if err != nil || val != sigreturn0 {
		return
    }
	val, err = readUintRaw(it.mem, uintptr(*ret+uint64(inst_size)), inst_size)
	if (err != nil || val != sigreturn4) {
		return
    }

	// Locate the address of the previous stack frame.

	val, err = readUintRaw(it.mem, uintptr(uint64(it.regs.CFA+sigreturnoffset)), int64(it.bi.Arch.PtrSize()))
	if err != nil {
		return
	}
	val += returnoffset
	val1, err := readUintRaw(it.mem, uintptr(val), int64(it.bi.Arch.PtrSize()))
	if err != nil {
		return
	}
	val += uint64(it.bi.Arch.PtrSize())

	// Read the pointer to the previous stack frame and update the CFA.

	val2, err := readUintRaw(it.mem, uintptr(val), int64(it.bi.Arch.PtrSize()))
	if err != nil {
        return
    }
	*ret = val2
	it.regs.CFA = int64(val1+uint64(it.bi.Arch.PtrSize()))
}
