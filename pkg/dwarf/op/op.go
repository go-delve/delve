package op

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

// Opcode represent a DWARF stack program instruction.
// See ./opcodes.go for a full list.
type Opcode byte

//go:generate go run ../../../_scripts/gen-opcodes.go opcodes.table opcodes.go

type stackfn func(Opcode, *context) error

type ReadMemoryFunc func([]byte, uint64) (int, error)

type context struct {
	buf     *bytes.Buffer
	prog    []byte
	stack   []int64
	pieces  []Piece
	ptrSize int

	DwarfRegisters
	readMemory ReadMemoryFunc
}

// Piece is a piece of memory stored either at an address or in a register.
type Piece struct {
	Size  int
	Kind  PieceKind
	Val   uint64
	Bytes []byte
}

// PieceKind describes the kind of a piece.
type PieceKind uint8

const (
	AddrPiece PieceKind = iota // The piece is stored in memory, Val is the address
	RegPiece                   // The piece is stored in a register, Val is the register number
	ImmPiece                   // The piece is an immediate value, Val or Bytes is the value
)

var (
	ErrStackUnderflow        = errors.New("DWARF stack underflow")
	ErrStackIndexOutOfBounds = errors.New("DWARF stack index out of bounds")
	ErrMemoryReadUnavailable = errors.New("memory read unavailable")
)

const arbitraryExecutionLimitFactor = 10

// ExecuteStackProgram executes a DWARF location expression and returns
// either an address (int64), or a slice of Pieces for location expressions
// that don't evaluate to an address (such as register and composite expressions).
func ExecuteStackProgram(regs DwarfRegisters, instructions []byte, ptrSize int, readMemory ReadMemoryFunc) (int64, []Piece, error) {
	ctxt := &context{
		buf:            bytes.NewBuffer(instructions),
		prog:           instructions,
		stack:          make([]int64, 0, 3),
		DwarfRegisters: regs,
		ptrSize:        ptrSize,
	}

	for tick := 0; tick < len(instructions)*arbitraryExecutionLimitFactor; tick++ {
		opcodeByte, err := ctxt.buf.ReadByte()
		if err != nil {
			break
		}
		opcode := Opcode(opcodeByte)
		if opcode == DW_OP_nop {
			continue
		}
		fn, ok := oplut[opcode]
		if !ok {
			return 0, nil, fmt.Errorf("invalid instruction %#v", opcode)
		}

		err = fn(opcode, ctxt)
		if err != nil {
			return 0, nil, err
		}
	}

	if ctxt.pieces != nil {
		if len(ctxt.pieces) == 1 && ctxt.pieces[0].Kind == RegPiece {
			return int64(regs.Uint64Val(ctxt.pieces[0].Val)), ctxt.pieces, nil
		}
		return 0, ctxt.pieces, nil
	}

	if len(ctxt.stack) == 0 {
		return 0, nil, errors.New("empty OP stack")
	}

	return ctxt.stack[len(ctxt.stack)-1], nil, nil
}

// PrettyPrint prints the DWARF stack program instructions to `out`.
func PrettyPrint(out io.Writer, instructions []byte) {
	in := bytes.NewBuffer(instructions)

	for {
		opcode, err := in.ReadByte()
		if err != nil {
			break
		}
		if name, hasname := opcodeName[Opcode(opcode)]; hasname {
			io.WriteString(out, name)
			out.Write([]byte{' '})
		} else {
			fmt.Fprintf(out, "%#x ", opcode)
		}
		for _, arg := range opcodeArgs[Opcode(opcode)] {
			switch arg {
			case 's':
				n, _ := util.DecodeSLEB128(in)
				fmt.Fprintf(out, "%#x ", n)
			case 'u':
				n, _ := util.DecodeULEB128(in)
				fmt.Fprintf(out, "%#x ", n)
			case '1':
				var x uint8
				binary.Read(in, binary.LittleEndian, &x)
				fmt.Fprintf(out, "%#x ", x)
			case '2':
				var x uint16
				binary.Read(in, binary.LittleEndian, &x)
				fmt.Fprintf(out, "%#x ", x)
			case '4':
				var x uint32
				binary.Read(in, binary.LittleEndian, &x)
				fmt.Fprintf(out, "%#x ", x)
			case '8':
				var x uint64
				binary.Read(in, binary.LittleEndian, &x)
				fmt.Fprintf(out, "%#x ", x)
			case 'B':
				sz, _ := util.DecodeULEB128(in)
				data := make([]byte, sz)
				sz2, _ := in.Read(data)
				data = data[:sz2]
				fmt.Fprintf(out, "%d [%x] ", sz, data)
			}
		}
	}
}

// closeLoc is called by opcodes that can only appear at the end of a
// location expression (DW_OP_regN, DW_OP_regx, DW_OP_stack_value...).
// It checks that we are at the end of the program or that the following
// opcode is DW_OP_piece or DW_OP_bit_piece and processes them.
func (ctxt *context) closeLoc(opcode0 Opcode, piece Piece) error {
	if ctxt.buf.Len() == 0 {
		ctxt.pieces = append(ctxt.pieces, piece)
		return nil
	}

	// DWARF doesn't say what happens to the operand stack at the end of a
	// location expression, resetting it here.
	ctxt.stack = ctxt.stack[:0]

	b, err := ctxt.buf.ReadByte()
	if err != nil {
		return err
	}
	opcode := Opcode(b)

	switch opcode {
	case DW_OP_piece:
		sz, _ := util.DecodeULEB128(ctxt.buf)
		piece.Size = int(sz)
		ctxt.pieces = append(ctxt.pieces, piece)
		return nil

	case DW_OP_bit_piece:
		// not supported
		return fmt.Errorf("invalid instruction %#v", opcode)
	default:
		return fmt.Errorf("invalid instruction %#v after %#v", opcode, opcode0)
	}
}

func callframecfa(opcode Opcode, ctxt *context) error {
	if ctxt.CFA == 0 {
		return fmt.Errorf("Could not retrieve CFA for current PC")
	}
	ctxt.stack = append(ctxt.stack, int64(ctxt.CFA))
	return nil
}

func addr(opcode Opcode, ctxt *context) error {
	buf := ctxt.buf.Next(ctxt.ptrSize)
	stack, err := util.ReadUintRaw(bytes.NewReader(buf), binary.LittleEndian, ctxt.ptrSize)
	if err != nil {
		return err
	}
	ctxt.stack = append(ctxt.stack, int64(stack+ctxt.StaticBase))
	return nil
}

func plusuconsts(opcode Opcode, ctxt *context) error {
	slen := len(ctxt.stack)
	num, _ := util.DecodeULEB128(ctxt.buf)
	ctxt.stack[slen-1] = ctxt.stack[slen-1] + int64(num)
	return nil
}

func consts(opcode Opcode, ctxt *context) error {
	num, _ := util.DecodeSLEB128(ctxt.buf)
	ctxt.stack = append(ctxt.stack, num)
	return nil
}

func framebase(opcode Opcode, ctxt *context) error {
	num, _ := util.DecodeSLEB128(ctxt.buf)
	ctxt.stack = append(ctxt.stack, ctxt.FrameBase+num)
	return nil
}

func register(opcode Opcode, ctxt *context) error {
	var regnum uint64
	if opcode == DW_OP_regx {
		regnum, _ = util.DecodeULEB128(ctxt.buf)
	} else {
		regnum = uint64(opcode - DW_OP_reg0)
	}
	return ctxt.closeLoc(opcode, Piece{Kind: RegPiece, Val: regnum})
}

func bregister(opcode Opcode, ctxt *context) error {
	var regnum uint64
	if opcode == DW_OP_bregx {
		regnum, _ = util.DecodeULEB128(ctxt.buf)
	} else {
		regnum = uint64(opcode - DW_OP_breg0)
	}
	offset, _ := util.DecodeSLEB128(ctxt.buf)
	if ctxt.Reg(regnum) == nil {
		return fmt.Errorf("register %d not available", regnum)
	}
	ctxt.stack = append(ctxt.stack, int64(ctxt.Uint64Val(regnum))+offset)
	return nil
}

func piece(opcode Opcode, ctxt *context) error {
	sz, _ := util.DecodeULEB128(ctxt.buf)

	if len(ctxt.stack) == 0 {
		// nothing on the stack means this piece is unavailable (padding,
		// optimized away...), see DWARFv4 sec. 2.6.1.3 page 30.
		ctxt.pieces = append(ctxt.pieces, Piece{Size: int(sz), Kind: ImmPiece, Val: 0})
		return nil
	}

	addr := ctxt.stack[len(ctxt.stack)-1]
	ctxt.pieces = append(ctxt.pieces, Piece{Size: int(sz), Kind: AddrPiece, Val: uint64(addr)})
	ctxt.stack = ctxt.stack[:0]
	return nil
}

func literal(opcode Opcode, ctxt *context) error {
	ctxt.stack = append(ctxt.stack, int64(opcode-DW_OP_lit0))
	return nil
}

func constnu(opcode Opcode, ctxt *context) error {
	var (
		n   uint64
		err error
	)
	switch opcode {
	case DW_OP_const1u:
		var b uint8
		b, err = ctxt.buf.ReadByte()
		n = uint64(b)
	case DW_OP_const2u:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 2)
	case DW_OP_const4u:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 4)
	case DW_OP_const8u:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 8)
	default:
		panic("internal error")
	}
	if err != nil {
		return err
	}
	ctxt.stack = append(ctxt.stack, int64(n))
	return nil
}

func constns(opcode Opcode, ctxt *context) error {
	var (
		n   uint64
		err error
	)
	switch opcode {
	case DW_OP_const1s:
		var b uint8
		b, err = ctxt.buf.ReadByte()
		n = uint64(int64(int8(b)))
	case DW_OP_const2s:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 2)
		n = uint64(int64(int16(n)))
	case DW_OP_const4s:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 4)
		n = uint64(int64(int32(n)))
	case DW_OP_const8s:
		n, err = util.ReadUintRaw(ctxt.buf, binary.LittleEndian, 8)
	default:
		panic("internal error")
	}
	if err != nil {
		return err
	}
	ctxt.stack = append(ctxt.stack, int64(n))
	return nil
}

func constu(opcode Opcode, ctxt *context) error {
	num, _ := util.DecodeULEB128(ctxt.buf)
	ctxt.stack = append(ctxt.stack, int64(num))
	return nil
}

func dup(_ Opcode, ctxt *context) error {
	if len(ctxt.stack) <= 0 {
		return ErrStackUnderflow
	}
	ctxt.stack = append(ctxt.stack, ctxt.stack[len(ctxt.stack)-1])
	return nil
}

func drop(_ Opcode, ctxt *context) error {
	if len(ctxt.stack) <= 0 {
		return ErrStackUnderflow
	}
	ctxt.stack = ctxt.stack[:len(ctxt.stack)-1]
	return nil
}

func pick(opcode Opcode, ctxt *context) error {
	var n byte
	switch opcode {
	case DW_OP_pick:
		n, _ = ctxt.buf.ReadByte()
	case DW_OP_over:
		n = 1
	default:
		panic("internal error")
	}
	idx := len(ctxt.stack) - 1 - int(uint8(n))
	if idx < 0 || idx >= len(ctxt.stack) {
		return ErrStackIndexOutOfBounds
	}
	ctxt.stack = append(ctxt.stack, ctxt.stack[idx])
	return nil
}

func swap(_ Opcode, ctxt *context) error {
	if len(ctxt.stack) < 2 {
		return ErrStackUnderflow
	}
	ctxt.stack[len(ctxt.stack)-1], ctxt.stack[len(ctxt.stack)-2] = ctxt.stack[len(ctxt.stack)-2], ctxt.stack[len(ctxt.stack)-1]
	return nil
}

func rot(_ Opcode, ctxt *context) error {
	if len(ctxt.stack) < 3 {
		return ErrStackUnderflow
	}
	ctxt.stack[len(ctxt.stack)-1], ctxt.stack[len(ctxt.stack)-2], ctxt.stack[len(ctxt.stack)-3] = ctxt.stack[len(ctxt.stack)-2], ctxt.stack[len(ctxt.stack)-3], ctxt.stack[len(ctxt.stack)-1]
	return nil
}

func unaryop(opcode Opcode, ctxt *context) error {
	if len(ctxt.stack) < 1 {
		return ErrStackUnderflow
	}
	operand := ctxt.stack[len(ctxt.stack)-1]
	switch opcode {
	case DW_OP_abs:
		if operand < 0 {
			operand = -operand
		}
	case DW_OP_neg:
		operand = -operand
	case DW_OP_not:
		operand = ^operand
	default:
		panic("internal error")
	}
	ctxt.stack[len(ctxt.stack)-1] = operand
	return nil
}

func binaryop(opcode Opcode, ctxt *context) error {
	if len(ctxt.stack) < 2 {
		return ErrStackUnderflow
	}
	second := ctxt.stack[len(ctxt.stack)-2]
	top := ctxt.stack[len(ctxt.stack)-1]
	var r int64
	ctxt.stack = ctxt.stack[:len(ctxt.stack)-2]
	switch opcode {
	case DW_OP_and:
		r = second & top
	case DW_OP_div:
		r = second / top
	case DW_OP_minus:
		r = second - top
	case DW_OP_mod:
		r = second % top
	case DW_OP_mul:
		r = second * top
	case DW_OP_or:
		r = second | top
	case DW_OP_plus:
		r = second + top
	case DW_OP_shl:
		r = second << uint64(top)
	case DW_OP_shr:
		r = second >> uint64(top)
	case DW_OP_shra:
		r = int64(uint64(second) >> uint64(top))
	case DW_OP_xor:
		r = second ^ top
	case DW_OP_le:
		r = bool2int(second <= top)
	case DW_OP_ge:
		r = bool2int(second >= top)
	case DW_OP_eq:
		r = bool2int(second == top)
	case DW_OP_lt:
		r = bool2int(second < top)
	case DW_OP_gt:
		r = bool2int(second > top)
	case DW_OP_ne:
		r = bool2int(second != top)
	default:
		panic("internal error")
	}
	ctxt.stack = append(ctxt.stack, r)
	return nil
}

func bool2int(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (ctxt *context) jump(n int16) error {
	i := len(ctxt.prog) - ctxt.buf.Len() + int(n)
	if i < 0 {
		return ErrStackUnderflow
	}
	if i >= len(ctxt.prog) {
		i = len(ctxt.prog)
	}
	ctxt.buf = bytes.NewBuffer(ctxt.prog[i:])
	return nil
}

func skip(_ Opcode, ctxt *context) error {
	var n int16
	binary.Read(ctxt.buf, binary.LittleEndian, &n)
	return ctxt.jump(n)
}

func bra(_ Opcode, ctxt *context) error {
	var n int16
	binary.Read(ctxt.buf, binary.LittleEndian, &n)

	if len(ctxt.stack) < 1 {
		return ErrStackUnderflow
	}
	top := ctxt.stack[len(ctxt.stack)-1]
	ctxt.stack = ctxt.stack[:len(ctxt.stack)-1]
	if top != 0 {
		return ctxt.jump(n)
	}
	return nil
}

func stackvalue(_ Opcode, ctxt *context) error {
	if len(ctxt.stack) < 1 {
		return ErrStackUnderflow
	}
	val := ctxt.stack[len(ctxt.stack)-1]
	ctxt.stack = ctxt.stack[:len(ctxt.stack)-1]
	return ctxt.closeLoc(DW_OP_stack_value, Piece{Kind: ImmPiece, Val: uint64(val)})
}

func implicitvalue(_ Opcode, ctxt *context) error {
	sz, _ := util.DecodeULEB128(ctxt.buf)
	block := make([]byte, sz)
	n, _ := ctxt.buf.Read(block)
	if uint64(n) != sz {
		return fmt.Errorf("insufficient bytes read while reading DW_OP_implicit_value's block %d (expected: %d)", n, sz)
	}
	return ctxt.closeLoc(DW_OP_implicit_value, Piece{Kind: ImmPiece, Bytes: block, Size: int(sz)})
}

func deref(op Opcode, ctxt *context) error {
	if ctxt.readMemory == nil {
		return ErrMemoryReadUnavailable
	}

	sz := ctxt.ptrSize
	if op == DW_OP_deref_size || op == DW_OP_xderef_size {
		n, err := ctxt.buf.ReadByte()
		if err != nil {
			return err
		}
		sz = int(n)
	}

	if len(ctxt.stack) <= 0 {
		return ErrStackUnderflow
	}

	addr := ctxt.stack[len(ctxt.stack)-1]
	ctxt.stack = ctxt.stack[:len(ctxt.stack)-1]

	if op == DW_OP_xderef || op == DW_OP_xderef_size {
		if len(ctxt.stack) <= 0 {
			return ErrStackUnderflow
		}
		// the second element on the stack is the "address space identifier" which we don't do anything with
		ctxt.stack = ctxt.stack[:len(ctxt.stack)-1]
	}

	buf := make([]byte, sz)
	_, err := ctxt.readMemory(buf, uint64(addr))
	if err != nil {
		return err
	}

	x, err := util.ReadUintRaw(bytes.NewReader(buf), binary.LittleEndian, sz)
	if err != nil {
		return err
	}

	ctxt.stack = append(ctxt.stack, int64(x))

	return nil
}
