package amd64util

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/go-delve/delve/pkg/proc"
)

// AMD64Xstate represents amd64 XSAVE area. See Section 13.1 (and
// following) of Intel® 64 and IA-32 Architectures Software Developer’s
// Manual, Volume 1: Basic Architecture.
type AMD64Xstate struct {
	AMD64PtraceFpRegs
	Xsave       []byte // raw xsave area
	AvxState    bool   // contains AVX state
	YmmSpace    [256]byte
	Avx512State bool // contains AVX512 state
	ZmmSpace    [512]byte
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

// Decode decodes an XSAVE area to a list of name/value pairs of registers.
func (xsave *AMD64Xstate) Decode() []proc.Register {
	var regs []proc.Register
	// x87 registers
	regs = proc.AppendUint64Register(regs, "CW", uint64(xsave.Cwd))
	regs = proc.AppendUint64Register(regs, "SW", uint64(xsave.Swd))
	regs = proc.AppendUint64Register(regs, "TW", uint64(xsave.Ftw))
	regs = proc.AppendUint64Register(regs, "FOP", uint64(xsave.Fop))
	regs = proc.AppendUint64Register(regs, "FIP", xsave.Rip)
	regs = proc.AppendUint64Register(regs, "FDP", xsave.Rdp)

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
		n := i / 16
		regs = proc.AppendBytesRegister(regs, fmt.Sprintf("XMM%d", n), xsave.XmmSpace[i:i+16])
		if xsave.AvxState {
			regs = proc.AppendBytesRegister(regs, fmt.Sprintf("YMM%d", n), xsave.YmmSpace[i:i+16])
			if xsave.Avx512State {
				regs = proc.AppendBytesRegister(regs, fmt.Sprintf("ZMM%d", n), xsave.ZmmSpace[n*32:(n+1)*32])
			}
		}
	}

	return regs
}

const (
	_XSTATE_MAX_KNOWN_SIZE = 2969

	_XSAVE_HEADER_START            = 512
	_XSAVE_HEADER_LEN              = 64
	_XSAVE_EXTENDED_REGION_START   = 576
	_XSAVE_SSE_REGION_LEN          = 416
	_XSAVE_AVX512_ZMM_REGION_START = 1152
)

// AMD64XstateRead reads a byte array containing an XSAVE area into regset.
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

	if xstate_bv&(1<<6) == 0 {
		// AVX512 state not present
		return nil
	}

	avx512state := xstateargs[_XSAVE_AVX512_ZMM_REGION_START:]
	regset.Avx512State = true
	copy(regset.ZmmSpace[:], avx512state[:len(regset.ZmmSpace)])

	// TODO(aarzilli): if xstate_bv&(1<<7) is set then xstateargs[1664:2688]
	// contains ZMM16 through ZMM31, those aren't just the higher 256bits, it's
	// the full register so each is 64 bytes (512bits)

	return nil
}
