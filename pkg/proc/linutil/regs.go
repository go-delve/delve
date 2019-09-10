package linutil

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/arch/x86/x86asm"

	"github.com/go-delve/delve/pkg/proc"
	"golang.org/x/arch/arm64/arm64asm"
)

// AMD64Registers implements the proc.Registers interface for the native/linux
// backend and core/linux backends, on AMD64.
type AMD64Registers struct {
	Regs     *AMD64PtraceRegs
	Fpregs   []proc.Register
	Fpregset *AMD64Xstate
}
type ARM64Registers struct {
	Regs     *ARM64PtraceRegs
	Fpregs   []proc.Register
	Fpregset *ARM64Xstate
}
// AMD64PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for AMD64 CPUs.
type AMD64PtraceRegs struct {
	R15      uint64
	R14      uint64
	R13      uint64
	R12      uint64
	Rbp      uint64
	Rbx      uint64
	R11      uint64
	R10      uint64
	R9       uint64
	R8       uint64
	Rax      uint64
	Rcx      uint64
	Rdx      uint64
	Rsi      uint64
	Rdi      uint64
	Orig_rax uint64
	Rip      uint64
	Cs       uint64
	Eflags   uint64
	Rsp      uint64
	Ss       uint64
	Fs_base  uint64
	Gs_base  uint64
	Ds       uint64
	Es       uint64
	Fs       uint64
	Gs       uint64
}
type ARM64PtraceRegs struct {
	Regs   [31]uint64
	Sp     uint64
	Pc     uint64
	Pstate uint64
}
// Slice returns the registers as a list of (name, value) pairs.
func (r *AMD64Registers) Slice(floatingPoint bool) []proc.Register {
	var regs = []struct {
		k string
		v uint64
	}{
		{"Rip", r.Regs.Rip},
		{"Rsp", r.Regs.Rsp},
		{"Rax", r.Regs.Rax},
		{"Rbx", r.Regs.Rbx},
		{"Rcx", r.Regs.Rcx},
		{"Rdx", r.Regs.Rdx},
		{"Rdi", r.Regs.Rdi},
		{"Rsi", r.Regs.Rsi},
		{"Rbp", r.Regs.Rbp},
		{"R8", r.Regs.R8},
		{"R9", r.Regs.R9},
		{"R10", r.Regs.R10},
		{"R11", r.Regs.R11},
		{"R12", r.Regs.R12},
		{"R13", r.Regs.R13},
		{"R14", r.Regs.R14},
		{"R15", r.Regs.R15},
		{"Orig_rax", r.Regs.Orig_rax},
		{"Cs", r.Regs.Cs},
		{"Eflags", r.Regs.Eflags},
		{"Ss", r.Regs.Ss},
		{"Fs_base", r.Regs.Fs_base},
		{"Gs_base", r.Regs.Gs_base},
		{"Ds", r.Regs.Ds},
		{"Es", r.Regs.Es},
		{"Fs", r.Regs.Fs},
		{"Gs", r.Regs.Gs},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.Fpregs))
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	if floatingPoint {
		out = append(out, r.Fpregs...)
	}
	return out
}
func (r *ARM64Registers) Slice(floatingPoint bool) []proc.Register {
	var regs = []struct {
		k string
		v uint64
	} {
		{"X30", r.Regs.Regs[30]},
		{"X29", r.Regs.Regs[29]},
		{"X28", r.Regs.Regs[28]},
		{"X27", r.Regs.Regs[27]},
		{"X26", r.Regs.Regs[26]},
		{"X25", r.Regs.Regs[25]},
		{"X24", r.Regs.Regs[24]},
		{"X23", r.Regs.Regs[23]},
		{"X22", r.Regs.Regs[22]},
		{"X21", r.Regs.Regs[21]},
		{"X20", r.Regs.Regs[20]},
		{"X19", r.Regs.Regs[19]},
		{"X18", r.Regs.Regs[18]},
		{"X17", r.Regs.Regs[17]},
		{"X16", r.Regs.Regs[16]},
		{"X15", r.Regs.Regs[15]},
		{"X14", r.Regs.Regs[14]},
		{"X13", r.Regs.Regs[13]},
		{"X12", r.Regs.Regs[12]},
		{"X11", r.Regs.Regs[11]},
		{"X10", r.Regs.Regs[10]},
		{"X9", r.Regs.Regs[9]},
		{"X8", r.Regs.Regs[8]},
		{"X7", r.Regs.Regs[7]},
		{"X6", r.Regs.Regs[6]},
		{"X5", r.Regs.Regs[5]},
		{"X4", r.Regs.Regs[4]},
		{"X3", r.Regs.Regs[3]},
		{"X2", r.Regs.Regs[2]},
		{"X1", r.Regs.Regs[1]},
		{"X0", r.Regs.Regs[0]},
		{"SP", r.Regs.Sp},
		{"PC", r.Regs.Pc},
		{"PState", r.Regs.Pstate},
	}
	out := make([]proc.Register, 0, len(regs))
	for _, reg := range regs {
		if reg.k == "Eflags" {
			out = proc.AppendEflagReg(out, reg.k, reg.v)
		} else {
			out = proc.AppendQwordReg(out, reg.k, reg.v)
		}
	}
	if floatingPoint {
		out = append(out, r.Fpregs...)
	}
	return out
}
// PC returns the value of RIP register.
func (r *AMD64Registers) PC() uint64 {
	return r.Regs.Rip
}
func (r *ARM64Registers) PC() uint64 {
	return r.Regs.Pc
}

// SP returns the value of RSP register.
func (r *AMD64Registers) SP() uint64 {
	return r.Regs.Rsp
}
func (r *ARM64Registers) SP() uint64 {
	return r.Regs.Sp
}

func (r *AMD64Registers) BP() uint64 {
	return r.Regs.Rbp
}
func (r *ARM64Registers) BP() uint64 {
	return r.Regs.Regs[30]
}
// CX returns the value of RCX register.
func (r *AMD64Registers) CX() uint64 {
	return r.Regs.Rcx
}
// CX returns the value of RCX register.
func (r *ARM64Registers) CX() uint64 {
	return r.Regs.Regs[29]
}
// TLS returns the address of the thread local storage memory segment.
func (r *AMD64Registers) TLS() uint64 {
	return r.Regs.Fs_base
}
func (r *ARM64Registers) TLS() uint64 {
	return r.Regs.Regs[0]
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *AMD64Registers) GAddr() (uint64, bool) {
	return 0, false
}
func (r *ARM64Registers) GAddr() (uint64, bool) {
	return 0, false
}
// Get returns the value of the n-th register (in x86asm order).
func (r *AMD64Registers) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000000ff
		mask16 = 0x0000ffff
		mask32 = 0xffffffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return r.Regs.Rax & mask8, nil
	case x86asm.CL:
		return r.Regs.Rcx & mask8, nil
	case x86asm.DL:
		return r.Regs.Rdx & mask8, nil
	case x86asm.BL:
		return r.Regs.Rbx & mask8, nil
	case x86asm.AH:
		return (r.Regs.Rax >> 8) & mask8, nil
	case x86asm.CH:
		return (r.Regs.Rcx >> 8) & mask8, nil
	case x86asm.DH:
		return (r.Regs.Rdx >> 8) & mask8, nil
	case x86asm.BH:
		return (r.Regs.Rbx >> 8) & mask8, nil
	case x86asm.SPB:
		return r.Regs.Rsp & mask8, nil
	case x86asm.BPB:
		return r.Regs.Rbp & mask8, nil
	case x86asm.SIB:
		return r.Regs.Rsi & mask8, nil
	case x86asm.DIB:
		return r.Regs.Rdi & mask8, nil
	case x86asm.R8B:
		return r.Regs.R8 & mask8, nil
	case x86asm.R9B:
		return r.Regs.R9 & mask8, nil
	case x86asm.R10B:
		return r.Regs.R10 & mask8, nil
	case x86asm.R11B:
		return r.Regs.R11 & mask8, nil
	case x86asm.R12B:
		return r.Regs.R12 & mask8, nil
	case x86asm.R13B:
		return r.Regs.R13 & mask8, nil
	case x86asm.R14B:
		return r.Regs.R14 & mask8, nil
	case x86asm.R15B:
		return r.Regs.R15 & mask8, nil

	// 16-bit
	case x86asm.AX:
		return r.Regs.Rax & mask16, nil
	case x86asm.CX:
		return r.Regs.Rcx & mask16, nil
	case x86asm.DX:
		return r.Regs.Rdx & mask16, nil
	case x86asm.BX:
		return r.Regs.Rbx & mask16, nil
	case x86asm.SP:
		return r.Regs.Rsp & mask16, nil
	case x86asm.BP:
		return r.Regs.Rbp & mask16, nil
	case x86asm.SI:
		return r.Regs.Rsi & mask16, nil
	case x86asm.DI:
		return r.Regs.Rdi & mask16, nil
	case x86asm.R8W:
		return r.Regs.R8 & mask16, nil
	case x86asm.R9W:
		return r.Regs.R9 & mask16, nil
	case x86asm.R10W:
		return r.Regs.R10 & mask16, nil
	case x86asm.R11W:
		return r.Regs.R11 & mask16, nil
	case x86asm.R12W:
		return r.Regs.R12 & mask16, nil
	case x86asm.R13W:
		return r.Regs.R13 & mask16, nil
	case x86asm.R14W:
		return r.Regs.R14 & mask16, nil
	case x86asm.R15W:
		return r.Regs.R15 & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return r.Regs.Rax & mask32, nil
	case x86asm.ECX:
		return r.Regs.Rcx & mask32, nil
	case x86asm.EDX:
		return r.Regs.Rdx & mask32, nil
	case x86asm.EBX:
		return r.Regs.Rbx & mask32, nil
	case x86asm.ESP:
		return r.Regs.Rsp & mask32, nil
	case x86asm.EBP:
		return r.Regs.Rbp & mask32, nil
	case x86asm.ESI:
		return r.Regs.Rsi & mask32, nil
	case x86asm.EDI:
		return r.Regs.Rdi & mask32, nil
	case x86asm.R8L:
		return r.Regs.R8 & mask32, nil
	case x86asm.R9L:
		return r.Regs.R9 & mask32, nil
	case x86asm.R10L:
		return r.Regs.R10 & mask32, nil
	case x86asm.R11L:
		return r.Regs.R11 & mask32, nil
	case x86asm.R12L:
		return r.Regs.R12 & mask32, nil
	case x86asm.R13L:
		return r.Regs.R13 & mask32, nil
	case x86asm.R14L:
		return r.Regs.R14 & mask32, nil
	case x86asm.R15L:
		return r.Regs.R15 & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return r.Regs.Rax, nil
	case x86asm.RCX:
		return r.Regs.Rcx, nil
	case x86asm.RDX:
		return r.Regs.Rdx, nil
	case x86asm.RBX:
		return r.Regs.Rbx, nil
	case x86asm.RSP:
		return r.Regs.Rsp, nil
	case x86asm.RBP:
		return r.Regs.Rbp, nil
	case x86asm.RSI:
		return r.Regs.Rsi, nil
	case x86asm.RDI:
		return r.Regs.Rdi, nil
	case x86asm.R8:
		return r.Regs.R8, nil
	case x86asm.R9:
		return r.Regs.R9, nil
	case x86asm.R10:
		return r.Regs.R10, nil
	case x86asm.R11:
		return r.Regs.R11, nil
	case x86asm.R12:
		return r.Regs.R12, nil
	case x86asm.R13:
		return r.Regs.R13, nil
	case x86asm.R14:
		return r.Regs.R14, nil
	case x86asm.R15:
		return r.Regs.R15, nil
	}

	return 0, proc.ErrUnknownRegister
}
func (r *ARM64Registers) Get(n int) (uint64, error) {	
	reg := arm64asm.Reg(n)
	const (
		mask32 = 0xffffffff
	)	
	switch reg {
	case arm64asm.W0:
		return 0, nil
	case arm64asm.X0:
		return r.Regs.Regs[0], nil
	case arm64asm.X1:
		return r.Regs.Regs[1], nil
	case arm64asm.X2:
		return r.Regs.Regs[2], nil
	case arm64asm.X3:
		return r.Regs.Regs[3], nil
	case arm64asm.X4:
		return r.Regs.Regs[4], nil
	case arm64asm.X5:
		return r.Regs.Regs[5], nil
	case arm64asm.X6:
		return r.Regs.Regs[6], nil
	case arm64asm.X7:
		return r.Regs.Regs[7], nil
	case arm64asm.X8:
		return r.Regs.Regs[8], nil
	case arm64asm.X9:
		return r.Regs.Regs[9], nil
	case arm64asm.X10:
		return r.Regs.Regs[10], nil
	case arm64asm.X11:
		return r.Regs.Regs[11], nil
	case arm64asm.X12:
		return r.Regs.Regs[12], nil
	case arm64asm.X13:
		return r.Regs.Regs[13], nil
	case arm64asm.X14:
		return r.Regs.Regs[14], nil
	case arm64asm.X15:
		return r.Regs.Regs[15], nil
	case arm64asm.X16:
		return r.Regs.Regs[16], nil
	case arm64asm.X17:
		return r.Regs.Regs[17], nil
	case arm64asm.X18:
		return r.Regs.Regs[18], nil
	case arm64asm.X19:
		return r.Regs.Regs[19], nil
	case arm64asm.X20:
		return r.Regs.Regs[20], nil
	case arm64asm.X21:
		return r.Regs.Regs[21], nil
	case arm64asm.X22:
		return r.Regs.Regs[22], nil
	case arm64asm.X23:
		return r.Regs.Regs[23], nil
	case arm64asm.X24:
		return r.Regs.Regs[24], nil
	case arm64asm.X25:
		return r.Regs.Regs[25], nil
	case arm64asm.X26:
		return r.Regs.Regs[26], nil
	case arm64asm.X27:
		return r.Regs.Regs[27], nil
	case arm64asm.X28:
		return r.Regs.Regs[28], nil
	case arm64asm.X29:
		return r.Regs.Regs[29], nil
	case arm64asm.X30:
		return r.Regs.Regs[30], nil
	case arm64asm.XZR:
		return r.Regs.Sp, nil
	}
	return 0, proc.ErrUnknownRegister
}

// Copy returns a copy of these registers that is guarenteed not to change.
func (r *AMD64Registers) Copy() proc.Registers {
	var rr AMD64Registers
	rr.Regs = &AMD64PtraceRegs{}
	rr.Fpregset = &AMD64Xstate{}
	*(rr.Regs) = *(r.Regs)
	if r.Fpregset != nil {
		*(rr.Fpregset) = *(r.Fpregset)
	}
	if r.Fpregs != nil {
		rr.Fpregs = make([]proc.Register, len(r.Fpregs))
		copy(rr.Fpregs, r.Fpregs)
	}
	return &rr
}
// Get returns the value of the n-th register (in arm64asm order).

// Copy returns a copy of these registers that is guarenteed not to change.
func (r *ARM64Registers) Copy() proc.Registers {
	var rr ARM64Registers
	rr.Regs = &ARM64PtraceRegs{}
	*(rr.Regs) = *(r.Regs)
	return &rr
}
// AMD64PtraceFpRegs tracks user_fpregs_struct in /usr/include/x86_64-linux-gnu/sys/user.h
type AMD64PtraceFpRegs struct {
	Cwd      uint16
	Swd      uint16
	Ftw      uint16
	Fop      uint16
	Rip      uint64
	Rdp      uint64
	Mxcsr    uint32
	MxcrMask uint32
	StSpace  [32]uint32
	XmmSpace [256]byte
	Padding  [24]uint32
}
type ARM64PtraceFpRegs struct {
	Vregs    [32][16]byte
	Fpsr     uint32
	Fpcr     uint32
	Reserved [2]uint32
}

// AMD64Xstate represents amd64 XSAVE area. See Section 13.1 (and
// following) of Intel® 64 and IA-32 Architectures Software Developer’s
// Manual, Volume 1: Basic Architecture.
type AMD64Xstate struct {
	AMD64PtraceFpRegs
	Xsave    []byte // raw xsave area
	AvxState bool   // contains AVX state
	YmmSpace [256]byte
}
// // AMD64Xstate represents amd64 XSAVE area. See Section 13.1 (and
// // following) of Intel® 64 and IA-32 Architectures Software Developer’s
// // Manual, Volume 1: Basic Architecture.
type ARM64Xstate struct {
	ARM64PtraceFpRegs
	Xsave    []byte // raw xsave area
	AvxState bool   // contains AVX state
	YmmSpace [256]byte
}
func (xsave *ARM64Xstate) Decode() (regs []proc.Register) {
	return
}
// Decode decodes an XSAVE area to a list of name/value pairs of registers.
func (xsave *AMD64Xstate) Decode() (regs []proc.Register) {
	// x87 registers
	regs = proc.AppendWordReg(regs, "CW", xsave.Cwd)
	regs = proc.AppendWordReg(regs, "SW", xsave.Swd)
	regs = proc.AppendWordReg(regs, "TW", xsave.Ftw)
	regs = proc.AppendWordReg(regs, "FOP", xsave.Fop)
	regs = proc.AppendQwordReg(regs, "FIP", xsave.Rip)
	regs = proc.AppendQwordReg(regs, "FDP", xsave.Rdp)

	for i := 0; i < len(xsave.StSpace); i += 4 {
		regs = proc.AppendX87Reg(regs, i/4, uint16(xsave.StSpace[i+2]), uint64(xsave.StSpace[i+1])<<32|uint64(xsave.StSpace[i]))
	}

	// SSE registers
	regs = proc.AppendMxcsrReg(regs, "MXCSR", uint64(xsave.Mxcsr))
	regs = proc.AppendDwordReg(regs, "MXCSR_MASK", xsave.MxcrMask)

	for i := 0; i < len(xsave.XmmSpace); i += 16 {
		regs = proc.AppendSSEReg(regs, fmt.Sprintf("XMM%d", i/16), xsave.XmmSpace[i:i+16])
		if xsave.AvxState {
			regs = proc.AppendSSEReg(regs, fmt.Sprintf("YMM%d", i/16), xsave.YmmSpace[i:i+16])
		}
	}

	return
}

const (
	_XSAVE_HEADER_START          = 512
	_XSAVE_HEADER_LEN            = 64
	_XSAVE_EXTENDED_REGION_START = 576
	_XSAVE_SSE_REGION_LEN        = 416
)

// LinuxX86XstateRead reads a byte array containing an XSAVE area into regset.
// If readLegacy is true regset.PtraceFpRegs will be filled with the
// contents of the legacy region of the XSAVE area.
// See Section 13.1 (and following) of Intel® 64 and IA-32 Architectures
// Software Developer’s Manual, Volume 1: Basic Architecture.
func AMD64XstateRead(xstateargs []byte, readLegacy bool, regset *AMD64Xstate) error {
	if _XSAVE_HEADER_START+_XSAVE_HEADER_LEN >= len(xstateargs) {
		return nil
	}
	if readLegacy {
		rdr := bytes.NewReader(xstateargs[:_XSAVE_HEADER_START])
		if err := binary.Read(rdr, binary.LittleEndian, &regset.AMD64PtraceFpRegs); err != nil {
			return err
		}
	}
	xsaveheader := xstateargs[_XSAVE_HEADER_START : _XSAVE_HEADER_START+_XSAVE_HEADER_LEN]
	xstate_bv := binary.LittleEndian.Uint64(xsaveheader[0:8])
	xcomp_bv := binary.LittleEndian.Uint64(xsaveheader[8:16])

	if xcomp_bv&(1<<63) != 0 {
		// compact format not supported
		return nil
	}

	if xstate_bv&(1<<2) == 0 {
		// AVX state not present
		return nil
	}

	avxstate := xstateargs[_XSAVE_EXTENDED_REGION_START:]
	regset.AvxState = true
	copy(regset.YmmSpace[:], avxstate[:len(regset.YmmSpace)])

	return nil
}
// // LinuxX86XstateRead reads a byte array containing an XSAVE area into regset.
// // If readLegacy is true regset.PtraceFpRegs will be filled with the
// // contents of the legacy region of the XSAVE area.
// // See Section 13.1 (and following) of Intel® 64 and IA-32 Architectures
// // Software Developer’s Manual, Volume 1: Basic Architecture.
func ARM64XstateRead(xstateargs []byte, readLegacy bool, regset *ARM64Xstate) error {
	if _XSAVE_HEADER_START+_XSAVE_HEADER_LEN >= len(xstateargs) {
		return nil
	}
	if readLegacy {
		rdr := bytes.NewReader(xstateargs[:_XSAVE_HEADER_START])
		if err := binary.Read(rdr, binary.LittleEndian, &regset.ARM64PtraceFpRegs); err != nil {
			return err
		}
	}
	xsaveheader := xstateargs[_XSAVE_HEADER_START : _XSAVE_HEADER_START+_XSAVE_HEADER_LEN]
	xstate_bv := binary.LittleEndian.Uint64(xsaveheader[0:8])
	xcomp_bv := binary.LittleEndian.Uint64(xsaveheader[8:16])

	if xcomp_bv&(1<<63) != 0 {
		// compact format not supported
		return nil
	}

	if xstate_bv&(1<<2) == 0 {
		// AVX state not present
		return nil
	}

	avxstate := xstateargs[_XSAVE_EXTENDED_REGION_START:]
	regset.AvxState = true
	copy(regset.YmmSpace[:], avxstate[:len(regset.YmmSpace)])

	return nil
}
