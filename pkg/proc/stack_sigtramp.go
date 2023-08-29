package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/logflags"
)

// readSigtrampgoContext reads runtime.sigtrampgo context at the specified address
func (it *stackIterator) readSigtrampgoContext() (*op.DwarfRegisters, error) {
	logger := logflags.DebuggerLogger()
	scope := FrameToScope(it.target, it.mem, it.g, 0, it.frame)
	bi := it.bi

	findvar := func(name string) *Variable {
		vars, _ := scope.Locals(0)
		for i := range vars {
			if vars[i].Name == name {
				return vars[i]
			}
		}
		return nil
	}

	deref := func(v *Variable) (uint64, error) {
		v.loadValue(loadSingleValue)
		if v.Unreadable != nil {
			return 0, fmt.Errorf("could not dereference %s: %v", v.Name, v.Unreadable)
		}
		if len(v.Children) < 1 {
			return 0, fmt.Errorf("could not dereference %s (no children?)", v.Name)
		}
		logger.Debugf("%s address is %#x", v.Name, v.Children[0].Addr)
		return v.Children[0].Addr, nil
	}

	getctxaddr := func() (uint64, error) {
		ctxvar := findvar("ctx")
		if ctxvar == nil {
			return 0, errors.New("ctx variable not found")
		}
		addr, err := deref(ctxvar)
		if err != nil {
			return 0, err
		}
		return addr, nil
	}

	switch bi.GOOS {
	case "windows":
		epvar := findvar("ep")
		if epvar == nil {
			return nil, errors.New("ep variable not found")
		}
		epaddr, err := deref(epvar)
		if err != nil {
			return nil, err
		}
		switch bi.Arch.Name {
		case "amd64":
			return sigtrampContextWindowsAMD64(it.mem, epaddr)
		case "arm64":
			return sigtrampContextWindowsARM64(it.mem, epaddr)
		default:
			return nil, errors.New("not implemented")
		}
	case "linux":
		addr, err := getctxaddr()
		if err != nil {
			return nil, err
		}

		switch bi.Arch.Name {
		case "386":
			return sigtrampContextLinux386(it.mem, addr)
		case "amd64":
			return sigtrampContextLinuxAMD64(it.mem, addr)
		case "arm64":
			return sigtrampContextLinuxARM64(it.mem, addr)
		default:
			return nil, errors.New("not implemented")
		}
	case "freebsd":
		addr, err := getctxaddr()
		if err != nil {
			return nil, err
		}
		return sigtrampContextFreebsdAMD64(it.mem, addr)
	case "darwin":
		addr, err := getctxaddr()
		if err != nil {
			return nil, err
		}
		switch bi.Arch.Name {
		case "amd64":
			return sigtrampContextDarwinAMD64(it.mem, addr)
		case "arm64":
			return sigtrampContextDarwinARM64(it.mem, addr)
		default:
			return nil, errors.New("not implemented")
		}
	default:
		return nil, errors.New("not implemented")
	}
}

func sigtrampContextLinuxAMD64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type stackt struct {
		ss_sp     uint64
		ss_flags  int32
		pad_cgo_0 [4]byte
		ss_size   uintptr
	}

	type mcontext struct {
		r8          uint64
		r9          uint64
		r10         uint64
		r11         uint64
		r12         uint64
		r13         uint64
		r14         uint64
		r15         uint64
		rdi         uint64
		rsi         uint64
		rbp         uint64
		rbx         uint64
		rdx         uint64
		rax         uint64
		rcx         uint64
		rsp         uint64
		rip         uint64
		eflags      uint64
		cs          uint16
		gs          uint16
		fs          uint16
		__pad0      uint16
		err         uint64
		trapno      uint64
		oldmask     uint64
		cr2         uint64
		fpstate     uint64 // pointer
		__reserved1 [8]uint64
	}

	type fpxreg struct {
		significand [4]uint16
		exponent    uint16
		padding     [3]uint16
	}

	type fpstate struct {
		cwd       uint16
		swd       uint16
		ftw       uint16
		fop       uint16
		rip       uint64
		rdp       uint64
		mxcsr     uint32
		mxcr_mask uint32
		_st       [8]fpxreg
		_xmm      [16][4]uint32
		padding   [24]uint32
	}

	type ucontext struct {
		uc_flags     uint64
		uc_link      uint64
		uc_stack     stackt
		uc_mcontext  mcontext
		uc_sigmask   [16]uint64
		__fpregs_mem fpstate
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	regs := &(((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext)
	dregs := make([]*op.DwarfRegister, regnum.AMD64MaxRegNum()+1)
	dregs[regnum.AMD64_R8] = op.DwarfRegisterFromUint64(regs.r8)
	dregs[regnum.AMD64_R9] = op.DwarfRegisterFromUint64(regs.r9)
	dregs[regnum.AMD64_R10] = op.DwarfRegisterFromUint64(regs.r10)
	dregs[regnum.AMD64_R11] = op.DwarfRegisterFromUint64(regs.r11)
	dregs[regnum.AMD64_R12] = op.DwarfRegisterFromUint64(regs.r12)
	dregs[regnum.AMD64_R13] = op.DwarfRegisterFromUint64(regs.r13)
	dregs[regnum.AMD64_R14] = op.DwarfRegisterFromUint64(regs.r14)
	dregs[regnum.AMD64_R15] = op.DwarfRegisterFromUint64(regs.r15)
	dregs[regnum.AMD64_Rdi] = op.DwarfRegisterFromUint64(regs.rdi)
	dregs[regnum.AMD64_Rsi] = op.DwarfRegisterFromUint64(regs.rsi)
	dregs[regnum.AMD64_Rbp] = op.DwarfRegisterFromUint64(regs.rbp)
	dregs[regnum.AMD64_Rbx] = op.DwarfRegisterFromUint64(regs.rbx)
	dregs[regnum.AMD64_Rdx] = op.DwarfRegisterFromUint64(regs.rdx)
	dregs[regnum.AMD64_Rax] = op.DwarfRegisterFromUint64(regs.rax)
	dregs[regnum.AMD64_Rcx] = op.DwarfRegisterFromUint64(regs.rcx)
	dregs[regnum.AMD64_Rsp] = op.DwarfRegisterFromUint64(regs.rsp)
	dregs[regnum.AMD64_Rip] = op.DwarfRegisterFromUint64(regs.rip)
	dregs[regnum.AMD64_Rflags] = op.DwarfRegisterFromUint64(regs.eflags)
	dregs[regnum.AMD64_Cs] = op.DwarfRegisterFromUint64(uint64(regs.cs))
	dregs[regnum.AMD64_Gs] = op.DwarfRegisterFromUint64(uint64(regs.gs))
	dregs[regnum.AMD64_Fs] = op.DwarfRegisterFromUint64(uint64(regs.fs))
	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0), nil
}

func sigtrampContextLinux386(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type sigcontext struct {
		gs            uint16
		__gsh         uint16
		fs            uint16
		__fsh         uint16
		es            uint16
		__esh         uint16
		ds            uint16
		__dsh         uint16
		edi           uint32
		esi           uint32
		ebp           uint32
		esp           uint32
		ebx           uint32
		edx           uint32
		ecx           uint32
		eax           uint32
		trapno        uint32
		err           uint32
		eip           uint32
		cs            uint16
		__csh         uint16
		eflags        uint32
		esp_at_signal uint32
		ss            uint16
		__ssh         uint16
		fpstate       uint32 // pointer
		oldmask       uint32
		cr2           uint32
	}

	type stackt struct {
		ss_sp    uint32 // pointer
		ss_flags int32
		ss_size  uint32
	}

	type ucontext struct {
		uc_flags    uint32
		uc_link     uint32 // pointer
		uc_stack    stackt
		uc_mcontext sigcontext
		uc_sigmask  uint32
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	regs := &(((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext)
	dregs := make([]*op.DwarfRegister, regnum.I386MaxRegNum()+1)
	dregs[regnum.I386_Gs] = op.DwarfRegisterFromUint64(uint64(regs.gs))
	dregs[regnum.I386_Fs] = op.DwarfRegisterFromUint64(uint64(regs.fs))
	dregs[regnum.I386_Es] = op.DwarfRegisterFromUint64(uint64(regs.es))
	dregs[regnum.I386_Ds] = op.DwarfRegisterFromUint64(uint64(regs.ds))
	dregs[regnum.I386_Edi] = op.DwarfRegisterFromUint64(uint64(regs.edi))
	dregs[regnum.I386_Esi] = op.DwarfRegisterFromUint64(uint64(regs.esi))
	dregs[regnum.I386_Ebp] = op.DwarfRegisterFromUint64(uint64(regs.ebp))
	dregs[regnum.I386_Esp] = op.DwarfRegisterFromUint64(uint64(regs.esp))
	dregs[regnum.I386_Ebx] = op.DwarfRegisterFromUint64(uint64(regs.ebx))
	dregs[regnum.I386_Edx] = op.DwarfRegisterFromUint64(uint64(regs.edx))
	dregs[regnum.I386_Ecx] = op.DwarfRegisterFromUint64(uint64(regs.ecx))
	dregs[regnum.I386_Eax] = op.DwarfRegisterFromUint64(uint64(regs.eax))
	dregs[regnum.I386_Eip] = op.DwarfRegisterFromUint64(uint64(regs.eip))
	dregs[regnum.I386_Cs] = op.DwarfRegisterFromUint64(uint64(regs.cs))
	dregs[regnum.I386_Ss] = op.DwarfRegisterFromUint64(uint64(regs.ss))
	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.I386_Eip, regnum.I386_Esp, regnum.I386_Ebp, 0), nil
}

func sigtrampContextLinuxARM64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type sigcontext struct {
		fault_address uint64
		regs          [31]uint64
		sp            uint64
		pc            uint64
		pstate        uint64
		_pad          [8]byte
		__reserved    [4096]byte
	}

	type stackt struct {
		ss_sp     uint64 // pointer
		ss_flags  int32
		pad_cgo_0 [4]byte
		ss_size   uint64
	}

	type ucontext struct {
		uc_flags    uint64
		uc_link     uint64 // pointer
		uc_stack    stackt
		uc_sigmask  uint64
		_pad        [(1024 - 64) / 8]byte
		_pad2       [8]byte
		uc_mcontext sigcontext
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	regs := &(((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext)
	dregs := make([]*op.DwarfRegister, regnum.ARM64MaxRegNum()+1)
	for i := range regs.regs {
		dregs[regnum.ARM64_X0+i] = op.DwarfRegisterFromUint64(regs.regs[i])
	}
	dregs[regnum.ARM64_SP] = op.DwarfRegisterFromUint64(regs.sp)
	dregs[regnum.ARM64_PC] = op.DwarfRegisterFromUint64(regs.pc)
	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.ARM64_PC, regnum.ARM64_SP, regnum.ARM64_BP, regnum.ARM64_LR), nil
}

func sigtrampContextFreebsdAMD64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type mcontext struct {
		mc_onstack       uint64
		mc_rdi           uint64
		mc_rsi           uint64
		mc_rdx           uint64
		mc_rcx           uint64
		mc_r8            uint64
		mc_r9            uint64
		mc_rax           uint64
		mc_rbx           uint64
		mc_rbp           uint64
		mc_r10           uint64
		mc_r11           uint64
		mc_r12           uint64
		mc_r13           uint64
		mc_r14           uint64
		mc_r15           uint64
		mc_trapno        uint32
		mc_fs            uint16
		mc_gs            uint16
		mc_addr          uint64
		mc_flags         uint32
		mc_es            uint16
		mc_ds            uint16
		mc_err           uint64
		mc_rip           uint64
		mc_cs            uint64
		mc_rflags        uint64
		mc_rsp           uint64
		mc_ss            uint64
		mc_len           uint64
		mc_fpformat      uint64
		mc_ownedfp       uint64
		mc_fpstate       [64]uint64
		mc_fsbase        uint64
		mc_gsbase        uint64
		mc_xfpustate     uint64
		mc_xfpustate_len uint64
		mc_spare         [4]uint64
	}

	type ucontext struct {
		uc_sigmask struct {
			__bits [4]uint32
		}
		uc_mcontext mcontext
		uc_link     uint64 // pointer
		uc_stack    struct {
			ss_sp     uintptr
			ss_size   uintptr
			ss_flags  int32
			pad_cgo_0 [4]byte
		}
		uc_flags  int32
		__spare__ [4]int32
		pad_cgo_0 [12]byte
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	mctxt := ((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext

	dregs := make([]*op.DwarfRegister, regnum.AMD64MaxRegNum()+1)

	dregs[regnum.AMD64_Rdi] = op.DwarfRegisterFromUint64(mctxt.mc_rdi)
	dregs[regnum.AMD64_Rsi] = op.DwarfRegisterFromUint64(mctxt.mc_rsi)
	dregs[regnum.AMD64_Rdx] = op.DwarfRegisterFromUint64(mctxt.mc_rdx)
	dregs[regnum.AMD64_Rcx] = op.DwarfRegisterFromUint64(mctxt.mc_rcx)
	dregs[regnum.AMD64_R8] = op.DwarfRegisterFromUint64(mctxt.mc_r8)
	dregs[regnum.AMD64_R9] = op.DwarfRegisterFromUint64(mctxt.mc_r9)
	dregs[regnum.AMD64_Rax] = op.DwarfRegisterFromUint64(mctxt.mc_rax)
	dregs[regnum.AMD64_Rbx] = op.DwarfRegisterFromUint64(mctxt.mc_rbx)
	dregs[regnum.AMD64_Rbp] = op.DwarfRegisterFromUint64(mctxt.mc_rbp)
	dregs[regnum.AMD64_R10] = op.DwarfRegisterFromUint64(mctxt.mc_r10)
	dregs[regnum.AMD64_R11] = op.DwarfRegisterFromUint64(mctxt.mc_r11)
	dregs[regnum.AMD64_R12] = op.DwarfRegisterFromUint64(mctxt.mc_r12)
	dregs[regnum.AMD64_R13] = op.DwarfRegisterFromUint64(mctxt.mc_r13)
	dregs[regnum.AMD64_R14] = op.DwarfRegisterFromUint64(mctxt.mc_r14)
	dregs[regnum.AMD64_R15] = op.DwarfRegisterFromUint64(mctxt.mc_r15)
	dregs[regnum.AMD64_Fs] = op.DwarfRegisterFromUint64(uint64(mctxt.mc_fs))
	dregs[regnum.AMD64_Gs] = op.DwarfRegisterFromUint64(uint64(mctxt.mc_gs))
	dregs[regnum.AMD64_Es] = op.DwarfRegisterFromUint64(uint64(mctxt.mc_es))
	dregs[regnum.AMD64_Ds] = op.DwarfRegisterFromUint64(uint64(mctxt.mc_ds))
	dregs[regnum.AMD64_Rip] = op.DwarfRegisterFromUint64(mctxt.mc_rip)
	dregs[regnum.AMD64_Cs] = op.DwarfRegisterFromUint64(mctxt.mc_cs)
	dregs[regnum.AMD64_Rflags] = op.DwarfRegisterFromUint64(mctxt.mc_rflags)
	dregs[regnum.AMD64_Rsp] = op.DwarfRegisterFromUint64(mctxt.mc_rsp)
	dregs[regnum.AMD64_Ss] = op.DwarfRegisterFromUint64(mctxt.mc_ss)

	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0), nil
}

func sigtrampContextFromExceptionPointers(mem MemoryReader, addr uint64) (uint64, error) {
	type exceptionpointers struct {
		record  uint64 // pointer
		context uint64 // pointer
	}
	buf := make([]byte, unsafe.Sizeof(exceptionpointers{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return 0, err
	}
	return ((*exceptionpointers)(unsafe.Pointer(&buf[0]))).context, nil

}

func sigtrampContextWindowsAMD64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type context struct {
		p1home         uint64
		p2home         uint64
		p3home         uint64
		p4home         uint64
		p5home         uint64
		p6home         uint64
		contextflags   uint32
		mxcsr          uint32
		segcs          uint16
		segds          uint16
		seges          uint16
		segfs          uint16
		seggs          uint16
		segss          uint16
		eflags         uint32
		dr0            uint64
		dr1            uint64
		dr2            uint64
		dr3            uint64
		dr6            uint64
		dr7            uint64
		rax            uint64
		rcx            uint64
		rdx            uint64
		rbx            uint64
		rsp            uint64
		rbp            uint64
		rsi            uint64
		rdi            uint64
		r8             uint64
		r9             uint64
		r10            uint64
		r11            uint64
		r12            uint64
		r13            uint64
		r14            uint64
		r15            uint64
		rip            uint64
		anon0          [512]byte
		vectorregister [26]struct {
			low  uint64
			high int64
		}
		vectorcontrol        uint64
		debugcontrol         uint64
		lastbranchtorip      uint64
		lastbranchfromrip    uint64
		lastexceptiontorip   uint64
		lastexceptionfromrip uint64
	}

	ctxtaddr, err := sigtrampContextFromExceptionPointers(mem, addr)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, unsafe.Sizeof(context{}))
	_, err = mem.ReadMemory(buf, ctxtaddr)
	if err != nil {
		return nil, fmt.Errorf("could not read context: %v", err)
	}
	ctxt := (*context)(unsafe.Pointer(&buf[0]))

	dregs := make([]*op.DwarfRegister, regnum.AMD64MaxRegNum()+1)

	dregs[regnum.AMD64_Cs] = op.DwarfRegisterFromUint64(uint64(ctxt.segcs))
	dregs[regnum.AMD64_Ds] = op.DwarfRegisterFromUint64(uint64(ctxt.segds))
	dregs[regnum.AMD64_Es] = op.DwarfRegisterFromUint64(uint64(ctxt.seges))
	dregs[regnum.AMD64_Fs] = op.DwarfRegisterFromUint64(uint64(ctxt.segfs))
	dregs[regnum.AMD64_Fs] = op.DwarfRegisterFromUint64(uint64(ctxt.seggs))
	dregs[regnum.AMD64_Ss] = op.DwarfRegisterFromUint64(uint64(ctxt.segss))
	dregs[regnum.AMD64_Rflags] = op.DwarfRegisterFromUint64(uint64(ctxt.eflags))
	dregs[regnum.AMD64_Rax] = op.DwarfRegisterFromUint64(ctxt.rax)
	dregs[regnum.AMD64_Rcx] = op.DwarfRegisterFromUint64(ctxt.rcx)
	dregs[regnum.AMD64_Rdx] = op.DwarfRegisterFromUint64(ctxt.rdx)
	dregs[regnum.AMD64_Rbx] = op.DwarfRegisterFromUint64(ctxt.rbx)
	dregs[regnum.AMD64_Rsp] = op.DwarfRegisterFromUint64(ctxt.rsp)
	dregs[regnum.AMD64_Rbp] = op.DwarfRegisterFromUint64(ctxt.rbp)
	dregs[regnum.AMD64_Rsi] = op.DwarfRegisterFromUint64(ctxt.rsi)
	dregs[regnum.AMD64_Rdi] = op.DwarfRegisterFromUint64(ctxt.rdi)
	dregs[regnum.AMD64_R8] = op.DwarfRegisterFromUint64(ctxt.r8)
	dregs[regnum.AMD64_R9] = op.DwarfRegisterFromUint64(ctxt.r9)
	dregs[regnum.AMD64_R10] = op.DwarfRegisterFromUint64(ctxt.r10)
	dregs[regnum.AMD64_R11] = op.DwarfRegisterFromUint64(ctxt.r11)
	dregs[regnum.AMD64_R12] = op.DwarfRegisterFromUint64(ctxt.r12)
	dregs[regnum.AMD64_R13] = op.DwarfRegisterFromUint64(ctxt.r13)
	dregs[regnum.AMD64_R14] = op.DwarfRegisterFromUint64(ctxt.r14)
	dregs[regnum.AMD64_R15] = op.DwarfRegisterFromUint64(ctxt.r15)
	dregs[regnum.AMD64_Rip] = op.DwarfRegisterFromUint64(ctxt.rip)

	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0), nil
}

func sigtrampContextWindowsARM64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type context struct {
		contextflags uint32
		cpsr         uint32
		x            [31]uint64 // fp is x[29], lr is x[30]
		xsp          uint64
		pc           uint64
		v            [32]struct {
			low  uint64
			high int64
		}
		fpcr uint32
		fpsr uint32
		bcr  [8]uint32
		bvr  [8]uint64
		wcr  [2]uint32
		wvr  [2]uint64
	}

	ctxtaddr, err := sigtrampContextFromExceptionPointers(mem, addr)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, unsafe.Sizeof(context{}))
	_, err = mem.ReadMemory(buf, ctxtaddr)
	if err != nil {
		return nil, fmt.Errorf("could not read context: %v", err)
	}
	ctxt := (*context)(unsafe.Pointer(&buf[0]))

	dregs := make([]*op.DwarfRegister, regnum.ARM64MaxRegNum()+1)
	for i := range ctxt.x {
		dregs[regnum.ARM64_X0+i] = op.DwarfRegisterFromUint64(ctxt.x[i])
	}
	dregs[regnum.ARM64_SP] = op.DwarfRegisterFromUint64(ctxt.xsp)
	dregs[regnum.ARM64_PC] = op.DwarfRegisterFromUint64(ctxt.pc)
	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.ARM64_PC, regnum.ARM64_SP, regnum.ARM64_BP, regnum.ARM64_LR), nil
}

func sigtrampContextDarwinAMD64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type ucontext struct {
		uc_onstack int32
		uc_sigmask uint32
		uc_stack   struct {
			ss_sp     uint64 // pointer
			ss_size   uintptr
			ss_flags  int32
			pad_cgo_0 [4]byte
		}
		uc_link     uint64 // pointer
		uc_mcsize   uint64
		uc_mcontext uint64 // pointer
	}

	type regmmst struct {
		mmst_reg  [10]int8
		mmst_rsrv [6]int8
	}

	type regxmm struct {
		xmm_reg [16]int8
	}

	type floatstate64 struct {
		fpu_reserved  [2]int32
		fpu_fcw       [2]byte
		fpu_fsw       [2]byte
		fpu_ftw       uint8
		fpu_rsrv1     uint8
		fpu_fop       uint16
		fpu_ip        uint32
		fpu_cs        uint16
		fpu_rsrv2     uint16
		fpu_dp        uint32
		fpu_ds        uint16
		fpu_rsrv3     uint16
		fpu_mxcsr     uint32
		fpu_mxcsrmask uint32
		fpu_stmm0     regmmst
		fpu_stmm1     regmmst
		fpu_stmm2     regmmst
		fpu_stmm3     regmmst
		fpu_stmm4     regmmst
		fpu_stmm5     regmmst
		fpu_stmm6     regmmst
		fpu_stmm7     regmmst
		fpu_xmm0      regxmm
		fpu_xmm1      regxmm
		fpu_xmm2      regxmm
		fpu_xmm3      regxmm
		fpu_xmm4      regxmm
		fpu_xmm5      regxmm
		fpu_xmm6      regxmm
		fpu_xmm7      regxmm
		fpu_xmm8      regxmm
		fpu_xmm9      regxmm
		fpu_xmm10     regxmm
		fpu_xmm11     regxmm
		fpu_xmm12     regxmm
		fpu_xmm13     regxmm
		fpu_xmm14     regxmm
		fpu_xmm15     regxmm
		fpu_rsrv4     [96]int8
		fpu_reserved1 int32
	}

	type regs64 struct {
		rax    uint64
		rbx    uint64
		rcx    uint64
		rdx    uint64
		rdi    uint64
		rsi    uint64
		rbp    uint64
		rsp    uint64
		r8     uint64
		r9     uint64
		r10    uint64
		r11    uint64
		r12    uint64
		r13    uint64
		r14    uint64
		r15    uint64
		rip    uint64
		rflags uint64
		cs     uint64
		fs     uint64
		gs     uint64
	}

	type mcontext64 struct {
		es struct {
			trapno     uint16
			cpu        uint16
			err        uint32
			faultvaddr uint64
		}
		ss        regs64
		fs        floatstate64
		pad_cgo_0 [4]byte
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	mctxtaddr := ((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext

	buf = make([]byte, unsafe.Sizeof(mcontext64{}))
	_, err = mem.ReadMemory(buf, mctxtaddr)
	if err != nil {
		return nil, err
	}

	ss := ((*mcontext64)(unsafe.Pointer(&buf[0]))).ss
	dregs := make([]*op.DwarfRegister, regnum.AMD64MaxRegNum()+1)
	dregs[regnum.AMD64_Rax] = op.DwarfRegisterFromUint64(ss.rax)
	dregs[regnum.AMD64_Rbx] = op.DwarfRegisterFromUint64(ss.rbx)
	dregs[regnum.AMD64_Rcx] = op.DwarfRegisterFromUint64(ss.rcx)
	dregs[regnum.AMD64_Rdx] = op.DwarfRegisterFromUint64(ss.rdx)
	dregs[regnum.AMD64_Rdi] = op.DwarfRegisterFromUint64(ss.rdi)
	dregs[regnum.AMD64_Rsi] = op.DwarfRegisterFromUint64(ss.rsi)
	dregs[regnum.AMD64_Rbp] = op.DwarfRegisterFromUint64(ss.rbp)
	dregs[regnum.AMD64_Rsp] = op.DwarfRegisterFromUint64(ss.rsp)
	dregs[regnum.AMD64_R8] = op.DwarfRegisterFromUint64(ss.r8)
	dregs[regnum.AMD64_R9] = op.DwarfRegisterFromUint64(ss.r9)
	dregs[regnum.AMD64_R10] = op.DwarfRegisterFromUint64(ss.r10)
	dregs[regnum.AMD64_R11] = op.DwarfRegisterFromUint64(ss.r11)
	dregs[regnum.AMD64_R12] = op.DwarfRegisterFromUint64(ss.r12)
	dregs[regnum.AMD64_R13] = op.DwarfRegisterFromUint64(ss.r13)
	dregs[regnum.AMD64_R14] = op.DwarfRegisterFromUint64(ss.r14)
	dregs[regnum.AMD64_R15] = op.DwarfRegisterFromUint64(ss.r15)
	dregs[regnum.AMD64_Rip] = op.DwarfRegisterFromUint64(ss.rip)
	dregs[regnum.AMD64_Rflags] = op.DwarfRegisterFromUint64(ss.rflags)
	dregs[regnum.AMD64_Cs] = op.DwarfRegisterFromUint64(ss.cs)
	dregs[regnum.AMD64_Fs] = op.DwarfRegisterFromUint64(ss.fs)
	dregs[regnum.AMD64_Gs] = op.DwarfRegisterFromUint64(ss.gs)

	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0), nil
}

func sigtrampContextDarwinARM64(mem MemoryReader, addr uint64) (*op.DwarfRegisters, error) {
	type ucontext struct {
		uc_onstack int32
		uc_sigmask uint32
		uc_stack   struct {
			ss_sp     uint64 // pointer
			ss_size   uintptr
			ss_flags  int32
			pad_cgo_0 [4]byte
		}
		uc_link     uint64 // pointer
		uc_mcsize   uint64
		uc_mcontext uint64 // pointer
	}

	type regs64 struct {
		x     [29]uint64 // registers x0 to x28
		fp    uint64     // frame register, x29
		lr    uint64     // link register, x30
		sp    uint64     // stack pointer, x31
		pc    uint64     // program counter
		cpsr  uint32     // current program status register
		__pad uint32
	}

	type mcontext64 struct {
		es struct {
			far uint64 // virtual fault addr
			esr uint32 // exception syndrome
			exc uint32 // number of arm exception taken
		}
		ss regs64
		ns struct {
			v    [64]uint64 // actually [32]uint128
			fpsr uint32
			fpcr uint32
		}
	}

	buf := make([]byte, unsafe.Sizeof(ucontext{}))
	_, err := mem.ReadMemory(buf, addr)
	if err != nil {
		return nil, err
	}
	mctxtaddr := ((*ucontext)(unsafe.Pointer(&buf[0]))).uc_mcontext

	buf = make([]byte, unsafe.Sizeof(mcontext64{}))
	_, err = mem.ReadMemory(buf, mctxtaddr)
	if err != nil {
		return nil, err
	}

	ss := ((*mcontext64)(unsafe.Pointer(&buf[0]))).ss
	dregs := make([]*op.DwarfRegister, regnum.ARM64MaxRegNum()+1)
	for i := range ss.x {
		dregs[regnum.ARM64_X0+i] = op.DwarfRegisterFromUint64(ss.x[i])
	}
	dregs[regnum.ARM64_BP] = op.DwarfRegisterFromUint64(ss.fp)
	dregs[regnum.ARM64_LR] = op.DwarfRegisterFromUint64(ss.lr)
	dregs[regnum.ARM64_SP] = op.DwarfRegisterFromUint64(ss.sp)
	dregs[regnum.ARM64_PC] = op.DwarfRegisterFromUint64(ss.pc)
	return op.NewDwarfRegisters(0, dregs, binary.LittleEndian, regnum.ARM64_PC, regnum.ARM64_SP, regnum.ARM64_BP, regnum.ARM64_LR), nil
}
