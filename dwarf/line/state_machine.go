package line

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/derekparker/dbg/dwarf/util"
)

type StateMachine struct {
	Dbl        *DebugLineInfo
	File       string
	Line       int
	Address    uint64
	Column     uint
	IsStmt     bool
	BasicBlock bool
	EndSeq     bool
}

type opcodefn func(*StateMachine, *bytes.Buffer)

// Special opcodes
const (
	DW_LNS_copy             = 1
	DW_LNS_advance_pc       = 2
	DW_LNS_advance_line     = 3
	DW_LNS_set_file         = 4
	DW_LNS_set_column       = 5
	DW_LNS_negate_stmt      = 6
	DW_LNS_set_basic_block  = 7
	DW_LNS_const_add_pc     = 8
	DW_LNS_fixed_advance_pc = 9
)

// Extended opcodes
const (
	DW_LINE_end_sequence = 1
	DW_LINE_set_address  = 2
	DW_LINE_define_file  = 3
)

var standardopcodes = map[byte]opcodefn{
	DW_LNS_copy:             copyfn,
	DW_LNS_advance_pc:       advancepc,
	DW_LNS_advance_line:     advanceline,
	DW_LNS_set_file:         setfile,
	DW_LNS_set_column:       setcolumn,
	DW_LNS_negate_stmt:      negatestmt,
	DW_LNS_set_basic_block:  setbasicblock,
	DW_LNS_const_add_pc:     constaddpc,
	DW_LNS_fixed_advance_pc: fixedadvancepc,
}

var extendedopcodes = map[byte]opcodefn{
	DW_LINE_end_sequence: endsequence,
	DW_LINE_set_address:  setaddress,
	DW_LINE_define_file:  definefile,
}

// Returns the filename, line number and PC for the next executable line in
// the traced program.
func (dbl *DebugLineInfo) NextLocAfterPC(pc uint64) (string, int, uint64) {
	var (
		sm  = &StateMachine{Dbl: dbl, File: dbl.FileNames[0].Name}
		buf = bytes.NewBuffer(dbl.Instructions)
	)

	for b, err := buf.ReadByte(); err == nil; b, err = buf.ReadByte() {
		switch {
		case b == 0:
			execExtendedOpcode(sm, b, buf)
		case b < dbl.Prologue.OpcodeBase:
			execStandardOpcode(sm, b, buf)
		default:
			execSpecialOpcode(sm, b)
		}

		if sm.Address > pc {
			advanceline(sm, buf)
			break
		}
	}

	return sm.File, sm.Line, sm.Address
}

func execSpecialOpcode(sm *StateMachine, instr byte) {
	var (
		opcode  = uint8(instr)
		decoded = opcode - sm.Dbl.Prologue.OpcodeBase
	)

	if sm.Dbl.Prologue.InitialIsStmt == uint8(1) {
		sm.IsStmt = true
	}

	sm.Line += int(sm.Dbl.Prologue.LineBase + int8(decoded%sm.Dbl.Prologue.LineRange))
	sm.Address += uint64(decoded / sm.Dbl.Prologue.LineRange)
	sm.BasicBlock = false
}

func execExtendedOpcode(sm *StateMachine, instr byte, buf *bytes.Buffer) {
	_, _ = util.DecodeULEB128(buf)
	b, _ := buf.ReadByte()
	fn, ok := extendedopcodes[b]
	if !ok {
		panic(fmt.Sprintf("Encountered unknown standard opcode %#v\n", b))
	}

	fn(sm, buf)
}

func execStandardOpcode(sm *StateMachine, instr byte, buf *bytes.Buffer) {
	fn, ok := standardopcodes[instr]
	if !ok {
		panic(fmt.Sprintf("Encountered unknown standard opcode %#v\n", instr))
	}

	fn(sm, buf)
}

func copyfn(sm *StateMachine, buf *bytes.Buffer) {
	sm.BasicBlock = false
}

func advancepc(sm *StateMachine, buf *bytes.Buffer) {
	addr, _ := util.DecodeULEB128(buf)
	sm.Address += addr * uint64(sm.Dbl.Prologue.MinInstrLength)
}

func advanceline(sm *StateMachine, buf *bytes.Buffer) {
	l, _ := util.DecodeSLEB128(buf)
	sm.Line += int(l)
}

func setfile(sm *StateMachine, buf *bytes.Buffer) {
	i, _ := util.DecodeULEB128(buf)
	sm.File = sm.Dbl.FileNames[i-1].Name
}

func setcolumn(sm *StateMachine, buf *bytes.Buffer) {
	c, _ := util.DecodeULEB128(buf)
	sm.Column = uint(c)
}

func negatestmt(sm *StateMachine, buf *bytes.Buffer) {
	sm.IsStmt = !sm.IsStmt
}

func setbasicblock(sm *StateMachine, buf *bytes.Buffer) {
	sm.BasicBlock = true
}

func constaddpc(sm *StateMachine, buf *bytes.Buffer) {
	sm.Address += (255 / uint64(sm.Dbl.Prologue.LineRange))
}

func fixedadvancepc(sm *StateMachine, buf *bytes.Buffer) {
	var operand uint16
	binary.Read(buf, binary.LittleEndian, &operand)

	sm.Address += uint64(operand)
}

func endsequence(sm *StateMachine, buf *bytes.Buffer) {
	sm.EndSeq = true
}

func setaddress(sm *StateMachine, buf *bytes.Buffer) {
	var addr uint64

	binary.Read(buf, binary.LittleEndian, &addr)

	sm.Address = addr
}

func definefile(sm *StateMachine, buf *bytes.Buffer) {
	var (
		_, _ = util.ParseString(buf)
		_, _ = util.DecodeULEB128(buf)
		_, _ = util.DecodeULEB128(buf)
		_, _ = util.DecodeULEB128(buf)
	)

	// Don't do anything here yet.
}
