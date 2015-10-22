package line

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/derekparker/delve/dwarf/util"
)

type Location struct {
	File    string
	Line    int
	Address uint64
	Delta   int
}

type StateMachine struct {
	dbl             *DebugLineInfo
	file            string
	line            int
	address         uint64
	column          uint
	isStmt          bool
	basicBlock      bool
	endSeq          bool
	lastWasStandard bool
	lastDelta       int
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

func newStateMachine(dbl *DebugLineInfo) *StateMachine {
	return &StateMachine{dbl: dbl, file: dbl.FileNames[0].Name, line: 1}
}

// Returns all PCs for a given file/line. Useful for loops where the 'for' line
// could be split amongst 2 PCs.
func (dbl *DebugLines) AllPCsForFileLine(f string, l int) (pcs []uint64) {
	var (
		foundFile bool
		lastAddr  uint64
		lineInfo  = dbl.GetLineInfo(f)
		sm        = newStateMachine(lineInfo)
		buf       = bytes.NewBuffer(lineInfo.Instructions)
	)

	for b, err := buf.ReadByte(); err == nil; b, err = buf.ReadByte() {
		findAndExecOpcode(sm, buf, b)
		if foundFile && sm.file != f {
			return
		}
		if sm.line == l && sm.file == f && sm.address != lastAddr {
			foundFile = true
			pcs = append(pcs, sm.address)
			line := sm.line
			// Keep going until we're on a different line. We only care about
			// when a line comes back around (i.e. for loop) so get to next line,
			// and try to find the line we care about again.
			for b, err := buf.ReadByte(); err == nil; b, err = buf.ReadByte() {
				findAndExecOpcode(sm, buf, b)
				if line < sm.line {
					break
				}
			}
		}
	}
	return
}

func (dbl *DebugLines) AllPCsBetween(begin, end uint64, filename string) []uint64 {
	lineInfo := dbl.GetLineInfo(filename)
	var (
		pcs      []uint64
		lastaddr uint64
		sm       = newStateMachine(lineInfo)
		buf      = bytes.NewBuffer(lineInfo.Instructions)
	)

	for b, err := buf.ReadByte(); err == nil; b, err = buf.ReadByte() {
		findAndExecOpcode(sm, buf, b)
		if sm.address > end {
			break
		}
		if sm.address >= begin && sm.address > lastaddr {
			lastaddr = sm.address
			pcs = append(pcs, sm.address)
		}
	}
	return pcs
}

func findAndExecOpcode(sm *StateMachine, buf *bytes.Buffer, b byte) {
	switch {
	case b == 0:
		execExtendedOpcode(sm, b, buf)
	case b < sm.dbl.Prologue.OpcodeBase:
		execStandardOpcode(sm, b, buf)
	default:
		execSpecialOpcode(sm, b)
	}
}

func execSpecialOpcode(sm *StateMachine, instr byte) {
	var (
		opcode  = uint8(instr)
		decoded = opcode - sm.dbl.Prologue.OpcodeBase
	)

	if sm.dbl.Prologue.InitialIsStmt == uint8(1) {
		sm.isStmt = true
	}

	sm.lastDelta = int(sm.dbl.Prologue.LineBase + int8(decoded%sm.dbl.Prologue.LineRange))
	sm.line += sm.lastDelta
	sm.address += uint64(decoded / sm.dbl.Prologue.LineRange)
	sm.basicBlock = false
	sm.lastWasStandard = false
}

func execExtendedOpcode(sm *StateMachine, instr byte, buf *bytes.Buffer) {
	_, _ = util.DecodeULEB128(buf)
	b, _ := buf.ReadByte()
	fn, ok := extendedopcodes[b]
	if !ok {
		panic(fmt.Sprintf("Encountered unknown extended opcode %#v\n", b))
	}
	sm.lastWasStandard = false

	fn(sm, buf)
}

func execStandardOpcode(sm *StateMachine, instr byte, buf *bytes.Buffer) {
	fn, ok := standardopcodes[instr]
	if !ok {
		panic(fmt.Sprintf("Encountered unknown standard opcode %#v\n", instr))
	}
	sm.lastWasStandard = true

	fn(sm, buf)
}

func copyfn(sm *StateMachine, buf *bytes.Buffer) {
	sm.basicBlock = false
}

func advancepc(sm *StateMachine, buf *bytes.Buffer) {
	addr, _ := util.DecodeULEB128(buf)
	sm.address += addr * uint64(sm.dbl.Prologue.MinInstrLength)
}

func advanceline(sm *StateMachine, buf *bytes.Buffer) {
	line, _ := util.DecodeSLEB128(buf)
	sm.line += int(line)
	sm.lastDelta = int(line)
}

func setfile(sm *StateMachine, buf *bytes.Buffer) {
	i, _ := util.DecodeULEB128(buf)
	sm.file = sm.dbl.FileNames[i-1].Name
}

func setcolumn(sm *StateMachine, buf *bytes.Buffer) {
	c, _ := util.DecodeULEB128(buf)
	sm.column = uint(c)
}

func negatestmt(sm *StateMachine, buf *bytes.Buffer) {
	sm.isStmt = !sm.isStmt
}

func setbasicblock(sm *StateMachine, buf *bytes.Buffer) {
	sm.basicBlock = true
}

func constaddpc(sm *StateMachine, buf *bytes.Buffer) {
	sm.address += (255 / uint64(sm.dbl.Prologue.LineRange))
}

func fixedadvancepc(sm *StateMachine, buf *bytes.Buffer) {
	var operand uint16
	binary.Read(buf, binary.LittleEndian, &operand)

	sm.address += uint64(operand)
}

func endsequence(sm *StateMachine, buf *bytes.Buffer) {
	sm.endSeq = true
}

func setaddress(sm *StateMachine, buf *bytes.Buffer) {
	var addr uint64

	binary.Read(buf, binary.LittleEndian, &addr)

	sm.address = addr
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
