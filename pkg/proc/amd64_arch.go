package proc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/frame"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
)

var amd64BreakInstruction = []byte{0xCC}

// AMD64Arch returns an initialized AMD64
// struct.
func AMD64Arch(goos string) *Arch {
	return &Arch{
		Name:                             "amd64",
		ptrSize:                          8,
		maxInstructionLength:             15,
		breakpointInstruction:            amd64BreakInstruction,
		breakInstrMovesPC:                true,
		derefTLS:                         goos == "windows",
		prologues:                        prologuesAMD64,
		fixFrameUnwindContext:            amd64FixFrameUnwindContext,
		switchStack:                      amd64SwitchStack,
		regSize:                          amd64RegSize,
		RegistersToDwarfRegisters:        amd64RegistersToDwarfRegisters,
		addrAndStackRegsToDwarfRegisters: amd64AddrAndStackRegsToDwarfRegisters,
		DwarfRegisterToString:            amd64DwarfRegisterToString,
		inhibitStepInto:                  func(*BinaryInfo, uint64) bool { return false },
		asmDecode:                        amd64AsmDecode,
		PCRegNum:                         regnum.AMD64_Rip,
		SPRegNum:                         regnum.AMD64_Rsp,
		BPRegNum:                         regnum.AMD64_Rbp,
		ContextRegNum:                    regnum.AMD64_Rdx,
		asmRegisters:                     amd64AsmRegisters,
		RegisterNameToDwarf:              nameToDwarfFunc(regnum.AMD64NameToDwarf),
	}
}

func amd64FixFrameUnwindContext(fctxt *frame.FrameContext, pc uint64, bi *BinaryInfo) *frame.FrameContext {
	a := bi.Arch
	if a.sigreturnfn == nil {
		a.sigreturnfn = bi.LookupFunc["runtime.sigreturn"]
	}

	if fctxt == nil || (a.sigreturnfn != nil && pc >= a.sigreturnfn.Entry && pc < a.sigreturnfn.End) {
		// When there's no frame descriptor entry use BP (the frame pointer) instead
		// - return register is [bp + a.PtrSize()] (i.e. [cfa-a.PtrSize()])
		// - cfa is bp + a.PtrSize()*2
		// - bp is [bp] (i.e. [cfa-a.PtrSize()*2])
		// - sp is cfa

		// When the signal handler runs it will move the execution to the signal
		// handling stack (installed using the sigaltstack system call).
		// This isn't a proper stack switch: the pointer to g in TLS will still
		// refer to whatever g was executing on that thread before the signal was
		// received.
		// Since go did not execute a stack switch the previous value of sp, pc
		// and bp is not saved inside g.sched, as it normally would.
		// The only way to recover is to either read sp/pc from the signal context
		// parameter (the ucontext_t* parameter) or to unconditionally follow the
		// frame pointer when we get to runtime.sigreturn (which is what we do
		// here).

		return &frame.FrameContext{
			RetAddrReg: regnum.AMD64_Rip,
			Regs: map[uint64]frame.DWRule{
				regnum.AMD64_Rip: {
					Rule:   frame.RuleOffset,
					Offset: int64(-a.PtrSize()),
				},
				regnum.AMD64_Rbp: {
					Rule:   frame.RuleOffset,
					Offset: int64(-2 * a.PtrSize()),
				},
				regnum.AMD64_Rsp: {
					Rule:   frame.RuleValOffset,
					Offset: 0,
				},
			},
			CFA: frame.DWRule{
				Rule:   frame.RuleCFA,
				Reg:    regnum.AMD64_Rbp,
				Offset: int64(2 * a.PtrSize()),
			},
		}
	}

	if a.crosscall2fn == nil {
		a.crosscall2fn = bi.LookupFunc["crosscall2"]
	}

	if a.crosscall2fn != nil && pc >= a.crosscall2fn.Entry && pc < a.crosscall2fn.End {
		rule := fctxt.CFA
		if rule.Offset == crosscall2SPOffsetBad {
			switch bi.GOOS {
			case "windows":
				rule.Offset += crosscall2SPOffsetWindows
			default:
				rule.Offset += crosscall2SPOffsetNonWindows
			}
		}
		fctxt.CFA = rule
	}

	// We assume that RBP is the frame pointer and we want to keep it updated,
	// so that we can use it to unwind the stack even when we encounter frames
	// without descriptor entries.
	// If there isn't a rule already we emit one.
	if fctxt.Regs[regnum.AMD64_Rbp].Rule == frame.RuleUndefined {
		fctxt.Regs[regnum.AMD64_Rbp] = frame.DWRule{
			Rule:   frame.RuleFramePointer,
			Reg:    regnum.AMD64_Rbp,
			Offset: 0,
		}
	}

	return fctxt
}

// cgocallSPOffsetSaveSlot is the offset from systemstack.SP where
// (goroutine.SP - StackHi) is saved in runtime.asmcgocall after the stack
// switch happens.
const amd64cgocallSPOffsetSaveSlot = 0x28

func amd64SwitchStack(it *stackIterator, _ *op.DwarfRegisters) bool {
	if it.frame.Current.Fn == nil {
		return false
	}
	switch it.frame.Current.Fn.Name {
	case "runtime.asmcgocall":
		if it.top || !it.systemstack {
			return false
		}

		// This function is called by a goroutine to execute a C function and
		// switches from the goroutine stack to the system stack.
		// Since we are unwinding the stack from callee to caller we have to switch
		// from the system stack to the goroutine stack.
		off, _ := readIntRaw(it.mem, uint64(it.regs.SP()+amd64cgocallSPOffsetSaveSlot), int64(it.bi.Arch.PtrSize())) // reads "offset of SP from StackHi" from where runtime.asmcgocall saved it
		oldsp := it.regs.SP()
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = uint64(int64(it.stackhi) - off)

		// runtime.asmcgocall can also be called from inside the system stack,
		// in that case no stack switch actually happens
		if it.regs.SP() == oldsp {
			return false
		}
		it.systemstack = false

		// advances to the next frame in the call stack
		it.frame.addrret = uint64(int64(it.regs.SP()) + int64(it.bi.Arch.PtrSize()))
		it.frame.Ret, _ = readUintRaw(it.mem, it.frame.addrret, int64(it.bi.Arch.PtrSize()))
		it.pc = it.frame.Ret

		it.top = false
		return true

	case "runtime.cgocallback_gofunc", "runtime.cgocallback":
		// For a detailed description of how this works read the long comment at
		// the start of $GOROOT/src/runtime/cgocall.go and the source code of
		// runtime.cgocallback_gofunc in $GOROOT/src/runtime/asm_amd64.s
		//
		// When a C functions calls back into go it will eventually call into
		// runtime.cgocallback_gofunc which is the function that does the stack
		// switch from the system stack back into the goroutine stack
		// Since we are going backwards on the stack here we see the transition
		// as goroutine stack -> system stack.

		if it.top || it.systemstack {
			return false
		}

		it.loadG0SchedSP()
		if it.g0_sched_sp <= 0 {
			return false
		}
		// entering the system stack
		it.regs.Reg(it.regs.SPRegNum).Uint64Val = it.g0_sched_sp
		// reads the previous value of g0.sched.sp that runtime.cgocallback_gofunc saved on the stack
		it.g0_sched_sp, _ = readUintRaw(it.mem, uint64(it.regs.SP()), int64(it.bi.Arch.PtrSize()))
		it.top = false
		callFrameRegs, ret, retaddr := it.advanceRegs()
		frameOnSystemStack := it.newStackframe(ret, retaddr)
		it.pc = frameOnSystemStack.Ret
		it.regs = callFrameRegs
		it.systemstack = true
		return true

	case "runtime.goexit", "runtime.rt0_go", "runtime.mcall":
		// Look for "top of stack" functions.
		it.atend = true
		return true

	case "runtime.mstart":
		// Calls to runtime.systemstack will switch to the systemstack then:
		// 1. alter the goroutine stack so that it looks like systemstack_switch
		//    was called
		// 2. alter the system stack so that it looks like the bottom-most frame
		//    belongs to runtime.mstart
		// If we find a runtime.mstart frame on the system stack of a goroutine
		// parked on runtime.systemstack_switch we assume runtime.systemstack was
		// called and continue tracing from the parked position.

		if it.top || !it.systemstack || it.g == nil {
			return false
		}
		if fn := it.bi.PCToFunc(it.g.PC); fn == nil || fn.Name != "runtime.systemstack_switch" {
			return false
		}

		it.switchToGoroutineStack()
		return true

	default:
		if it.systemstack && it.top && it.g != nil && strings.HasPrefix(it.frame.Current.Fn.Name, "runtime.") && it.frame.Current.Fn.Name != "runtime.throw" {
			// The runtime switches to the system stack in multiple places.
			// This usually happens through a call to runtime.systemstack but there
			// are functions that switch to the system stack manually (for example
			// runtime.morestack).
			// Since we are only interested in printing the system stack for cgo
			// calls we switch directly to the goroutine stack if we detect that the
			// function at the top of the stack is a runtime function.
			//
			// The function "runtime.throw" is deliberately excluded from this
			// because it can end up in the stack during a cgo call and switching to
			// the goroutine stack will exclude all the C functions from the stack
			// trace.
			it.switchToGoroutineStack()
			return true
		}

		return false
	}
}

// amd64RegSize returns the size (in bytes) of register regnum.
// The mapping between hardware registers and DWARF registers is specified
// in the System V ABI AMD64 Architecture Processor Supplement page 57,
// figure 3.36
// https://www.uclibc.org/docs/psABI-x86_64.pdf
func amd64RegSize(rn uint64) int {
	// XMM registers
	if rn > regnum.AMD64_Rip && rn <= 32 {
		return 16
	}
	// x87 registers
	if rn >= 33 && rn <= 40 {
		return 10
	}
	return 8
}

func amd64RegistersToDwarfRegisters(staticBase uint64, regs Registers) *op.DwarfRegisters {
	dregs := initDwarfRegistersFromSlice(int(regnum.AMD64MaxRegNum()), regs, regnum.AMD64NameToDwarf)
	dr := op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0)
	dr.SetLoadMoreCallback(loadMoreDwarfRegistersFromSliceFunc(dr, regs, regnum.AMD64NameToDwarf))
	return dr
}

func initDwarfRegistersFromSlice(maxRegs int, regs Registers, nameToDwarf map[string]int) []*op.DwarfRegister {
	dregs := make([]*op.DwarfRegister, maxRegs+1)
	regslice, _ := regs.Slice(false)
	for _, reg := range regslice {
		if dwarfReg, ok := nameToDwarf[strings.ToLower(reg.Name)]; ok {
			dregs[dwarfReg] = reg.Reg
		}
	}
	return dregs
}

func loadMoreDwarfRegistersFromSliceFunc(dr *op.DwarfRegisters, regs Registers, nameToDwarf map[string]int) func() {
	return func() {
		regslice, err := regs.Slice(true)
		dr.FloatLoadError = err
		for _, reg := range regslice {
			name := strings.ToLower(reg.Name)
			if dwarfReg, ok := nameToDwarf[name]; ok {
				dr.AddReg(uint64(dwarfReg), reg.Reg)
			} else if reg.Reg.Bytes != nil && (strings.HasPrefix(name, "ymm") || strings.HasPrefix(name, "zmm")) {
				xmmIdx, ok := nameToDwarf["x"+name[1:]]
				if !ok {
					continue
				}
				xmmReg := dr.Reg(uint64(xmmIdx))
				if xmmReg == nil || xmmReg.Bytes == nil {
					continue
				}
				nb := make([]byte, 0, len(xmmReg.Bytes)+len(reg.Reg.Bytes))
				nb = append(nb, xmmReg.Bytes...)
				nb = append(nb, reg.Reg.Bytes...)
				xmmReg.Bytes = nb
			}
		}
	}
}

func amd64AddrAndStackRegsToDwarfRegisters(staticBase, pc, sp, bp, lr uint64) op.DwarfRegisters {
	dregs := make([]*op.DwarfRegister, regnum.AMD64_Rip+1)
	dregs[regnum.AMD64_Rip] = op.DwarfRegisterFromUint64(pc)
	dregs[regnum.AMD64_Rsp] = op.DwarfRegisterFromUint64(sp)
	dregs[regnum.AMD64_Rbp] = op.DwarfRegisterFromUint64(bp)

	return *op.NewDwarfRegisters(staticBase, dregs, binary.LittleEndian, regnum.AMD64_Rip, regnum.AMD64_Rsp, regnum.AMD64_Rbp, 0)
}

func amd64DwarfRegisterToString(i int, reg *op.DwarfRegister) (name string, floatingPoint bool, repr string) {
	name = regnum.AMD64ToName(uint64(i))

	if reg == nil {
		return name, false, ""
	}

	switch n := strings.ToLower(name); n {
	case "rflags":
		return name, false, eflagsDescription.Describe(reg.Uint64Val, 64)

	case "cw", "sw", "tw", "fop":
		return name, true, fmt.Sprintf("%#04x", reg.Uint64Val)

	case "mxcsr_mask":
		return name, true, fmt.Sprintf("%#08x", reg.Uint64Val)

	case "mxcsr":
		return name, true, mxcsrDescription.Describe(reg.Uint64Val, 32)

	default:
		if reg.Bytes != nil && strings.HasPrefix(n, "xmm") {
			return name, true, formatSSEReg(name, reg.Bytes)
		} else if reg.Bytes != nil && strings.HasPrefix(n, "st(") {
			return name, true, formatX87Reg(reg.Bytes)
		} else if reg.Bytes == nil || (reg.Bytes != nil && len(reg.Bytes) <= 8) {
			return name, false, fmt.Sprintf("%#016x", reg.Uint64Val)
		} else {
			return name, false, fmt.Sprintf("%#x", reg.Bytes)
		}
	}
}

func formatSSEReg(name string, reg []byte) string {
	out := new(bytes.Buffer)
	formatSSERegInternal(reg, out)
	if len(reg) < 32 {
		return out.String()
	}

	fmt.Fprintf(out, "\n\t[%sh] ", "Y"+name[1:])
	formatSSERegInternal(reg[16:], out)

	if len(reg) < 64 {
		return out.String()
	}

	fmt.Fprintf(out, "\n\t[%shl] ", "Z"+name[1:])
	formatSSERegInternal(reg[32:], out)
	fmt.Fprintf(out, "\n\t[%shh] ", "Z"+name[1:])
	formatSSERegInternal(reg[48:], out)

	return out.String()
}

func formatSSERegInternal(xmm []byte, out *bytes.Buffer) {
	buf := bytes.NewReader(xmm)

	var vi [16]uint8
	for i := range vi {
		binary.Read(buf, binary.LittleEndian, &vi[i])
	}

	fmt.Fprintf(out, "0x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x", vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8], vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

	fmt.Fprintf(out, "\tv2_int={ %02x%02x%02x%02x%02x%02x%02x%02x %02x%02x%02x%02x%02x%02x%02x%02x }", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0], vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8])

	fmt.Fprintf(out, "\tv4_int={ %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x }", vi[3], vi[2], vi[1], vi[0], vi[7], vi[6], vi[5], vi[4], vi[11], vi[10], vi[9], vi[8], vi[15], vi[14], vi[13], vi[12])

	fmt.Fprintf(out, "\tv8_int={ %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x }", vi[1], vi[0], vi[3], vi[2], vi[5], vi[4], vi[7], vi[6], vi[9], vi[8], vi[11], vi[10], vi[13], vi[12], vi[15], vi[14])

	fmt.Fprintf(out, "\tv16_int={ %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x }", vi[0], vi[1], vi[2], vi[3], vi[4], vi[5], vi[6], vi[7], vi[8], vi[9], vi[10], vi[11], vi[12], vi[13], vi[14], vi[15])

	buf.Seek(0, io.SeekStart)
	var v2 [2]float64
	for i := range v2 {
		binary.Read(buf, binary.LittleEndian, &v2[i])
	}
	fmt.Fprintf(out, "\tv2_float={ %g %g }", v2[0], v2[1])

	buf.Seek(0, io.SeekStart)
	var v4 [4]float32
	for i := range v4 {
		binary.Read(buf, binary.LittleEndian, &v4[i])
	}
	fmt.Fprintf(out, "\tv4_float={ %g %g %g %g }", v4[0], v4[1], v4[2], v4[3])
}

func formatX87Reg(b []byte) string {
	if len(b) < 10 {
		return fmt.Sprintf("%#x", b)
	}
	mantissa := binary.LittleEndian.Uint64(b[:8])
	exponent := uint16(binary.LittleEndian.Uint16(b[8:]))

	var f float64
	fset := false

	const (
		_SIGNBIT    = 1 << 15
		_EXP_BIAS   = (1 << 14) - 1 // 2^(n-1) - 1 = 16383
		_SPECIALEXP = (1 << 15) - 1 // all bits set
		_HIGHBIT    = 1 << 63
		_QUIETBIT   = 1 << 62
	)

	sign := 1.0
	if exponent&_SIGNBIT != 0 {
		sign = -1.0
	}
	exponent &= ^uint16(_SIGNBIT)

	NaN := math.NaN()
	Inf := math.Inf(+1)

	switch exponent {
	case 0:
		switch {
		case mantissa == 0:
			f = sign * 0.0
			fset = true
		case mantissa&_HIGHBIT != 0:
			f = NaN
			fset = true
		}
	case _SPECIALEXP:
		switch {
		case mantissa&_HIGHBIT == 0:
			f = sign * Inf
			fset = true
		default:
			f = NaN // signaling NaN
			fset = true
		}
	default:
		if mantissa&_HIGHBIT == 0 {
			f = NaN
			fset = true
		}
	}

	if !fset {
		significand := float64(mantissa) / (1 << 63)
		f = sign * math.Ldexp(significand, int(exponent-_EXP_BIAS))
	}

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, exponent)
	binary.Write(&buf, binary.LittleEndian, mantissa)

	return fmt.Sprintf("%#04x%016x\t%g", exponent, mantissa, f)
}
