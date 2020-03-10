package linutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/go-delve/delve/pkg/proc"
	"golang.org/x/arch/x86/x86asm"
)

// I386Registers implements the proc.Registers interface for the native/linux
// backend and core/linux backends, on I386.
type I386Registers struct {
	Regs     *I386PtraceRegs
	Fpregs   []proc.Register
	Fpregset *I386Xstate
	Tls      uint64
}

// I386PtraceRegs is the struct used by the linux kernel to return the
// general purpose registers for I386 CPUs.
type I386PtraceRegs struct {
	Ebx      int32
	Ecx      int32
	Edx      int32
	Esi      int32
	Edi      int32
	Ebp      int32
	Eax      int32
	Xds      int32
	Xes      int32
	Xfs      int32
	Xgs      int32
	Orig_eax int32
	Eip      int32
	Xcs      int32
	Eflags   int32
	Esp      int32
	Xss      int32
}

// Slice returns the registers as a list of (name, value) pairs.
func (r *I386Registers) Slice(floatingPoint bool) []proc.Register {
	var regs = []struct {
		k string
		v int32
	}{
		{"Ebx", r.Regs.Ebx},
		{"Ecx", r.Regs.Ecx},
		{"Edx", r.Regs.Edx},
		{"Esi", r.Regs.Esi},
		{"Edi", r.Regs.Edi},
		{"Ebp", r.Regs.Ebp},
		{"Eax", r.Regs.Eax},
		{"Xds", r.Regs.Xds},
		{"Xes", r.Regs.Xes},
		{"Xfs", r.Regs.Xfs},
		{"Xgs", r.Regs.Xgs},
		{"Orig_eax", r.Regs.Orig_eax},
		{"Eip", r.Regs.Eip},
		{"Xcs", r.Regs.Xcs},
		{"Eflags", r.Regs.Eflags},
		{"Esp", r.Regs.Esp},
		{"Xss", r.Regs.Xss},
	}
	out := make([]proc.Register, 0, len(regs)+len(r.Fpregs))
	for _, reg := range regs {
		out = proc.AppendUint64Register(out, reg.k, uint64(uint32(reg.v)))
	}
	if floatingPoint {
		out = append(out, r.Fpregs...)
	}
	return out
}

// PC returns the value of EIP register.
func (r *I386Registers) PC() uint64 {
	return uint64(uint32(r.Regs.Eip))
}

// SP returns the value of ESP register.
func (r *I386Registers) SP() uint64 {
	return uint64(uint32(r.Regs.Esp))
}

func (r *I386Registers) BP() uint64 {
	return uint64(uint32(r.Regs.Ebp))
}

// CX returns the value of ECX register.
func (r *I386Registers) CX() uint64 {
	return uint64(uint32(r.Regs.Ecx))
}

// TLS returns the address of the thread local storage memory segment.
func (r I386Registers) TLS() uint64 {
	return r.Tls
}

// GAddr returns the address of the G variable if it is known, 0 and false
// otherwise.
func (r *I386Registers) GAddr() (uint64, bool) {
	return 0, false
}

// Get returns the value of the n-th register (in x86asm order).
func (r *I386Registers) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000000ff
		mask16 = 0x0000ffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return uint64(r.Regs.Eax) & mask8, nil
	case x86asm.CL:
		return uint64(r.Regs.Ecx) & mask8, nil
	case x86asm.DL:
		return uint64(r.Regs.Edx) & mask8, nil
	case x86asm.BL:
		return uint64(r.Regs.Ebx) & mask8, nil
	case x86asm.AH:
		return (uint64(r.Regs.Eax) >> 8) & mask8, nil
	case x86asm.CH:
		return (uint64(r.Regs.Ecx) >> 8) & mask8, nil
	case x86asm.DH:
		return (uint64(r.Regs.Edx) >> 8) & mask8, nil
	case x86asm.BH:
		return (uint64(r.Regs.Ebx) >> 8) & mask8, nil
	case x86asm.SPB:
		return uint64(r.Regs.Esp) & mask8, nil
	case x86asm.BPB:
		return uint64(r.Regs.Ebp) & mask8, nil
	case x86asm.SIB:
		return uint64(r.Regs.Esi) & mask8, nil
	case x86asm.DIB:
		return uint64(r.Regs.Edi) & mask8, nil

	// 16-bit
	case x86asm.AX:
		return uint64(r.Regs.Eax) & mask16, nil
	case x86asm.CX:
		return uint64(r.Regs.Ecx) & mask16, nil
	case x86asm.DX:
		return uint64(r.Regs.Edx) & mask16, nil
	case x86asm.BX:
		return uint64(r.Regs.Ebx) & mask16, nil
	case x86asm.SP:
		return uint64(r.Regs.Esp) & mask16, nil
	case x86asm.BP:
		return uint64(r.Regs.Ebp) & mask16, nil
	case x86asm.SI:
		return uint64(r.Regs.Esi) & mask16, nil
	case x86asm.DI:
		return uint64(r.Regs.Edi) & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return uint64(uint32(r.Regs.Eax)), nil
	case x86asm.ECX:
		return uint64(uint32(r.Regs.Ecx)), nil
	case x86asm.EDX:
		return uint64(uint32(r.Regs.Edx)), nil
	case x86asm.EBX:
		return uint64(uint32(r.Regs.Ebx)), nil
	case x86asm.ESP:
		return uint64(uint32(r.Regs.Esp)), nil
	case x86asm.EBP:
		return uint64(uint32(r.Regs.Ebp)), nil
	case x86asm.ESI:
		return uint64(uint32(r.Regs.Esi)), nil
	case x86asm.EDI:
		return uint64(uint32(r.Regs.Edi)), nil
	}
	return 0, proc.ErrUnknownRegister
}

// Copy returns a copy of these registers that is guarenteed not to change.
func (r *I386Registers) Copy() proc.Registers {
	var rr I386Registers
	rr.Regs = &I386PtraceRegs{}
	rr.Fpregset = &I386Xstate{}
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

// I386PtraceFpRegs tracks user_fpregs_struct in /usr/include/x86_64-linux-gnu/sys/user.h
type I386PtraceFpRegs struct {
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

// I386Xstate represents amd64 XSAVE area. See Section 13.1 (and
// following) of Intel® 64 and IA-32 Architectures Software Developer’s
// Manual, Volume 1: Basic Architecture.
type I386Xstate struct {
	I386PtraceFpRegs
	Xsave    []byte // raw xsave area
	AvxState bool   // contains AVX state
	YmmSpace [256]byte
}

// Decode decodes an XSAVE area to a list of name/value pairs of registers.
func (xsave *I386Xstate) Decode() (regs []proc.Register) {
	// x87 registers
	regs = proc.AppendUint64Register(regs, "CW", uint64(xsave.Cwd))
	regs = proc.AppendUint64Register(regs, "SW", uint64(xsave.Swd))
	regs = proc.AppendUint64Register(regs, "TW", uint64(xsave.Ftw))
	regs = proc.AppendUint64Register(regs, "FOP", uint64(xsave.Fop))
	regs = proc.AppendUint64Register(regs, "FIP", uint64(xsave.Rip))
	regs = proc.AppendUint64Register(regs, "FDP", uint64(xsave.Rdp))

	for i := 0; i < len(xsave.StSpace); i += 4 {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint64(xsave.StSpace[i+1])<<32|uint64(xsave.StSpace[i]))
		binary.Write(&buf, binary.LittleEndian, uint16(xsave.StSpace[i+2]))
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("ST(%d)", i/4), buf.Bytes())
	}

	// SSE registers
	regs = proc.AppendUint64Register(regs, "MXCSR", uint64(xsave.Mxcsr))
	regs = proc.AppendUint64Register(regs, "MXCSR_MASK", uint64(xsave.MxcrMask))

	for i := 0; i < len(xsave.XmmSpace); i += 16 {
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("XMM%d", i/16), xsave.XmmSpace[i:i+16])
		if xsave.AvxState {
			regs = proc.AppendBytesRegister(regs, fmt.Sprintf("YMM%d", i/16), xsave.YmmSpace[i:i+16])
		}
	}

	return
}

// LinuxX86XstateRead reads a byte array containing an XSAVE area into regset.
// If readLegacy is true regset.PtraceFpRegs will be filled with the
// contents of the legacy region of the XSAVE area.
// See Section 13.1 (and following) of Intel® 64 and IA-32 Architectures
// Software Developer’s Manual, Volume 1: Basic Architecture.
func I386XstateRead(xstateargs []byte, readLegacy bool, regset *I386Xstate) error {
	if _XSAVE_HEADER_START+_XSAVE_HEADER_LEN >= len(xstateargs) {
		return nil
	}
	if readLegacy {
		rdr := bytes.NewReader(xstateargs[:_XSAVE_HEADER_START])
		if err := binary.Read(rdr, binary.LittleEndian, &regset.I386PtraceFpRegs); err != nil {
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
