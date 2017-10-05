package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

// Registers is an interface for a generic register type. The
// interface encapsulates the generic values / actions
// we need independent of arch. The concrete register types
// will be different depending on OS/Arch.
type Registers interface {
	PC() uint64
	SP() uint64
	BP() uint64
	CX() uint64
	TLS() uint64
	// GAddr returns the address of the G variable if it is known, 0 and false otherwise
	GAddr() (uint64, bool)
	Get(int) (uint64, error)
	SetPC(Thread, uint64) error
	Slice() []Register
}

type Register struct {
	Name  string
	Bytes []byte
	Value string
}

// AppendWordReg appends a word (16 bit) register to regs.
func AppendWordReg(regs []Register, name string, value uint16) []Register {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, value)
	return append(regs, Register{name, buf.Bytes(), fmt.Sprintf("%#04x", value)})
}

// AppendDwordReg appends a double word (32 bit) register to regs.
func AppendDwordReg(regs []Register, name string, value uint32) []Register {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, value)
	return append(regs, Register{name, buf.Bytes(), fmt.Sprintf("%#08x", value)})
}

// AppendQwordReg appends a quad word (64 bit) register to regs.
func AppendQwordReg(regs []Register, name string, value uint64) []Register {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, value)
	return append(regs, Register{name, buf.Bytes(), fmt.Sprintf("%#016x", value)})
}

func appendFlagReg(regs []Register, name string, value uint64, descr flagRegisterDescr, size int) []Register {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, value)
	return append(regs, Register{name, buf.Bytes()[:size], descr.Describe(value, size)})
}

// AppendEflagReg appends EFLAG register to regs.
func AppendEflagReg(regs []Register, name string, value uint64) []Register {
	return appendFlagReg(regs, name, value, eflagsDescription, 64)
}

// AppendMxcsrReg appends MXCSR register to regs.
func AppendMxcsrReg(regs []Register, name string, value uint64) []Register {
	return appendFlagReg(regs, name, value, mxcsrDescription, 32)
}

// AppendX87Reg appends a 80 bit float register to regs.
func AppendX87Reg(regs []Register, index int, exponent uint16, mantissa uint64) []Register {
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

	return append(regs, Register{fmt.Sprintf("ST(%d)", index), buf.Bytes(), fmt.Sprintf("%#04x%016x\t%g", exponent, mantissa, f)})
}

// AppendSSEReg appends a 256 bit SSE register to regs.
func AppendSSEReg(regs []Register, name string, xmm []byte) []Register {
	buf := bytes.NewReader(xmm)

	var out bytes.Buffer
	var vi [16]uint8
	for i := range vi {
		binary.Read(buf, binary.LittleEndian, &vi[i])
	}

	fmt.Fprintf(&out, "0x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x%02x", vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8], vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0])

	fmt.Fprintf(&out, "\tv2_int={ %02x%02x%02x%02x%02x%02x%02x%02x %02x%02x%02x%02x%02x%02x%02x%02x }", vi[7], vi[6], vi[5], vi[4], vi[3], vi[2], vi[1], vi[0], vi[15], vi[14], vi[13], vi[12], vi[11], vi[10], vi[9], vi[8])

	fmt.Fprintf(&out, "\tv4_int={ %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x %02x%02x%02x%02x }", vi[3], vi[2], vi[1], vi[0], vi[7], vi[6], vi[5], vi[4], vi[11], vi[10], vi[9], vi[8], vi[15], vi[14], vi[13], vi[12])

	fmt.Fprintf(&out, "\tv8_int={ %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x %02x%02x }", vi[1], vi[0], vi[3], vi[2], vi[5], vi[4], vi[7], vi[6], vi[9], vi[8], vi[11], vi[10], vi[13], vi[12], vi[15], vi[14])

	fmt.Fprintf(&out, "\tv16_int={ %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x }", vi[0], vi[1], vi[2], vi[3], vi[4], vi[5], vi[6], vi[7], vi[8], vi[9], vi[10], vi[11], vi[12], vi[13], vi[14], vi[15])

	buf.Seek(0, os.SEEK_SET)
	var v2 [2]float64
	for i := range v2 {
		binary.Read(buf, binary.LittleEndian, &v2[i])
	}
	fmt.Fprintf(&out, "\tv2_float={ %g %g }", v2[0], v2[1])

	buf.Seek(0, os.SEEK_SET)
	var v4 [4]float32
	for i := range v4 {
		binary.Read(buf, binary.LittleEndian, &v4[i])
	}
	fmt.Fprintf(&out, "\tv4_float={ %g %g %g %g }", v4[0], v4[1], v4[2], v4[3])

	return append(regs, Register{name, xmm, out.String()})
}

var UnknownRegisterError = errors.New("unknown register")

type flagRegisterDescr []flagDescr
type flagDescr struct {
	name string
	mask uint64
}

var mxcsrDescription flagRegisterDescr = []flagDescr{
	{"FZ", 1 << 15},
	{"RZ/RN", 1<<14 | 1<<13},
	{"PM", 1 << 12},
	{"UM", 1 << 11},
	{"OM", 1 << 10},
	{"ZM", 1 << 9},
	{"DM", 1 << 8},
	{"IM", 1 << 7},
	{"DAZ", 1 << 6},
	{"PE", 1 << 5},
	{"UE", 1 << 4},
	{"OE", 1 << 3},
	{"ZE", 1 << 2},
	{"DE", 1 << 1},
	{"IE", 1 << 0},
}

var eflagsDescription flagRegisterDescr = []flagDescr{
	{"CF", 1 << 0},
	{"", 1 << 1},
	{"PF", 1 << 2},
	{"AF", 1 << 4},
	{"ZF", 1 << 6},
	{"SF", 1 << 7},
	{"TF", 1 << 8},
	{"IF", 1 << 9},
	{"DF", 1 << 10},
	{"OF", 1 << 11},
	{"IOPL", 1<<12 | 1<<13},
	{"NT", 1 << 14},
	{"RF", 1 << 16},
	{"VM", 1 << 17},
	{"AC", 1 << 18},
	{"VIF", 1 << 19},
	{"VIP", 1 << 20},
	{"ID", 1 << 21},
}

func (descr flagRegisterDescr) Mask() uint64 {
	var r uint64
	for _, f := range descr {
		r = r | f.mask
	}
	return r
}

func (descr flagRegisterDescr) Describe(reg uint64, bitsize int) string {
	var r []string
	for _, f := range descr {
		if f.name == "" {
			continue
		}
		// rbm is f.mask with only the right-most bit set:
		// 0001 1100 -> 0000 0100
		rbm := f.mask & -f.mask
		if rbm == f.mask {
			if reg&f.mask != 0 {
				r = append(r, f.name)
			}
		} else {
			x := (reg & f.mask) >> uint64(math.Log2(float64(rbm)))
			r = append(r, fmt.Sprintf("%s=%x", f.name, x))
		}
	}
	if reg & ^descr.Mask() != 0 {
		r = append(r, fmt.Sprintf("unknown_flags=%x", reg&^descr.Mask()))
	}
	return fmt.Sprintf("%#0*x\t[%s]", bitsize/4, reg, strings.Join(r, " "))
}

// tracks user_fpregs_struct in /usr/include/x86_64-linux-gnu/sys/user.h
type PtraceFpRegs struct {
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

// LinuxX86Xstate represents amd64 XSAVE area. See Section 13.1 (and
// following) of Intel® 64 and IA-32 Architectures Software Developer’s
// Manual, Volume 1: Basic Architecture.
type LinuxX86Xstate struct {
	PtraceFpRegs
	AvxState bool // contains AVX state
	YmmSpace [256]byte
}

// Decode decodes an XSAVE area to a list of name/value pairs of registers.
func (xsave *LinuxX86Xstate) Decode() (regs []Register) {
	// x87 registers
	regs = AppendWordReg(regs, "CW", xsave.Cwd)
	regs = AppendWordReg(regs, "SW", xsave.Swd)
	regs = AppendWordReg(regs, "TW", xsave.Ftw)
	regs = AppendWordReg(regs, "FOP", xsave.Fop)
	regs = AppendQwordReg(regs, "FIP", xsave.Rip)
	regs = AppendQwordReg(regs, "FDP", xsave.Rdp)

	for i := 0; i < len(xsave.StSpace); i += 4 {
		regs = AppendX87Reg(regs, i/4, uint16(xsave.StSpace[i+2]), uint64(xsave.StSpace[i+1])<<32|uint64(xsave.StSpace[i]))
	}

	// SSE registers
	regs = AppendMxcsrReg(regs, "MXCSR", uint64(xsave.Mxcsr))
	regs = AppendDwordReg(regs, "MXCSR_MASK", xsave.MxcrMask)

	for i := 0; i < len(xsave.XmmSpace); i += 16 {
		regs = AppendSSEReg(regs, fmt.Sprintf("XMM%d", i/16), xsave.XmmSpace[i:i+16])
		if xsave.AvxState {
			regs = AppendSSEReg(regs, fmt.Sprintf("YMM%d", i/16), xsave.YmmSpace[i:i+16])
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
func LinuxX86XstateRead(xstateargs []byte, readLegacy bool, regset *LinuxX86Xstate) error {
	if _XSAVE_HEADER_START+_XSAVE_HEADER_LEN >= len(xstateargs) {
		return nil
	}
	if readLegacy {
		rdr := bytes.NewReader(xstateargs[:_XSAVE_HEADER_START])
		if err := binary.Read(rdr, binary.LittleEndian, &regset.PtraceFpRegs); err != nil {
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
