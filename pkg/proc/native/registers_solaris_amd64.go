package native

// #include <libproc.h>
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/solutil"
)

// SetPC sets RIP to the value specified by 'pc'.
func (thread *nativeThread) setPC(pc uint64) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*solutil.AMD64Registers)
	r.Regs.Rip = int64(pc)
	thread.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Plwp_setregs(thread.dbp.os.pr, C.uint(thread.ID), (*C.long)(unsafe.Pointer(&r.Regs))); rc == -1 {
			err = err1
		}
	})
	return err
}

// SetReg changes the value of the specified register.
func (thread *nativeThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	ir, err := registers(thread)
	if err != nil {
		return err
	}
	r := ir.(*solutil.AMD64Registers)
	switch regNum {
	case regnum.AMD64_Rip:
		r.Regs.Rip = int64(reg.Uint64Val)
	case regnum.AMD64_Rsp:
		r.Regs.Rsp = int64(reg.Uint64Val)
	case regnum.AMD64_Rdx:
		r.Regs.Rdx = int64(reg.Uint64Val)
	default:
		return fmt.Errorf("changing register %d not implemented", regNum)
	}
	thread.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Plwp_setregs(thread.dbp.os.pr, C.uint(thread.ID), (*C.long)(unsafe.Pointer(&r.Regs))); rc == -1 {
			err = err1
		}
	})
	return err
}

func registers(thread *nativeThread) (proc.Registers, error) {
	var (
		err error
		regs solutil.AMD64Regset
	)
	thread.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Plwp_getregs(thread.dbp.os.pr, C.uint(thread.ID), (*C.long)(unsafe.Pointer(&regs))); rc == -1 {
			err = err1
		}
	})
	if err != nil {
		return nil, err
	}
	r := solutil.NewAMD64Registers(regs, func(r *solutil.AMD64Registers) (err error) {
		var fpregset solutil.AMD64Fpregset
		var floatLoadError error
		r.Fpregs, fpregset, floatLoadError = thread.fpRegisters()
		r.Fpregset = &fpregset
		return floatLoadError
	})
	return r, nil
}

func (thread *nativeThread) fpRegisters() (regs []proc.Register, fpregs solutil.AMD64Fpregset, err error) {
	thread.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Plwp_getfpregs(thread.dbp.os.pr, C.uint(thread.ID), (*C.prfpregset_t)(unsafe.Pointer(&fpregs))); rc == -1 {
			err = err1
		}
	})
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %w", err)
	} else {
		regs = fpregs.Decode()
	}
	return
}
