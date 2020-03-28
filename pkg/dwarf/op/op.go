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

//go:generate go run ../../../scripts/gen-opcodes.go opcodes.table opcodes.go

type stackfn func(Opcode, *context) error

type context struct {
	buf     *bytes.Buffer
	stack   []int64
	pieces  []Piece
	reg     bool
	ptrSize int

	DwarfRegisters
}

// Piece is a piece of memory stored either at an address or in a register.
type Piece struct {
	Size       int
	Addr       int64
	RegNum     uint64
	IsRegister bool
}

// ExecuteStackProgram executes a DWARF location expression and returns
// either an address (int64), or a slice of Pieces for location expressions
// that don't evaluate to an address (such as register and composite expressions).
func ExecuteStackProgram(regs DwarfRegisters, instructions []byte, ptrSize int) (int64, []Piece, error) {
	ctxt := &context{
		buf:            bytes.NewBuffer(instructions),
		stack:          make([]int64, 0, 3),
		DwarfRegisters: regs,
		ptrSize:        ptrSize,
	}

	for {
		opcodeByte, err := ctxt.buf.ReadByte()
		if err != nil {
			break
		}
		opcode := Opcode(opcodeByte)
		if ctxt.reg && opcode != DW_OP_piece {
			break
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

func plus(opcode Opcode, ctxt *context) error {
	var (
		slen   = len(ctxt.stack)
		digits = ctxt.stack[slen-2 : slen]
		st     = ctxt.stack[:slen-2]
	)

	ctxt.stack = append(st, digits[0]+digits[1])
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
	ctxt.reg = true
	if opcode == DW_OP_regx {
		n, _ := util.DecodeSLEB128(ctxt.buf)
		ctxt.pieces = append(ctxt.pieces, Piece{IsRegister: true, RegNum: uint64(n)})
	} else {
		ctxt.pieces = append(ctxt.pieces, Piece{IsRegister: true, RegNum: uint64(opcode - DW_OP_reg0)})
	}
	return nil
}

func piece(opcode Opcode, ctxt *context) error {
	sz, _ := util.DecodeULEB128(ctxt.buf)
	if ctxt.reg {
		ctxt.reg = false
		ctxt.pieces[len(ctxt.pieces)-1].Size = int(sz)
		return nil
	}

	if len(ctxt.stack) == 0 {
		return errors.New("empty OP stack")
	}

	addr := ctxt.stack[len(ctxt.stack)-1]
	ctxt.pieces = append(ctxt.pieces, Piece{Size: int(sz), Addr: addr})
	ctxt.stack = ctxt.stack[:0]
	return nil
}
