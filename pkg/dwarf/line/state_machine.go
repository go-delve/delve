package line

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

type Location struct {
	File    string
	Line    int
	Address uint64
	Delta   int
}

type StateMachine struct {
	dbl           *DebugLineInfo
	file          string
	line          int
	address       uint64
	column        uint
	isStmt        bool
	isa           uint64 // instruction set architecture register (DWARFv4)
	basicBlock    bool
	endSeq        bool
	lastDelta     int
	prologueEnd   bool
	epilogueBegin bool
	// valid is true if the current value of the state machine is the address of
	// an instruction (using the terminology used by DWARF spec the current
	// value of the state machine should be appended to the matrix representing
	// the compilation unit)
	valid bool

	started bool

	buf     *bytes.Buffer // remaining instructions
	opcodes []opcodefn

	definedFiles []*FileEntry // files defined with DW_LINE_define_file

	lastAddress uint64
	lastFile    string
	lastLine    int
}

type opcodeKind uint8

const (
	specialOpcode opcodeKind = iota
	standardOpcode
	extendedOpcode
)

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
	DW_LNS_prologue_end     = 10
	DW_LNS_epilogue_begin   = 11
	DW_LNS_set_isa          = 12
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
	DW_LNS_prologue_end:     prologueend,
	DW_LNS_epilogue_begin:   epiloguebegin,
	DW_LNS_set_isa:          setisa,
}

var extendedopcodes = map[byte]opcodefn{
	DW_LINE_end_sequence: endsequence,
	DW_LINE_set_address:  setaddress,
	DW_LINE_define_file:  definefile,
}

func newStateMachine(dbl *DebugLineInfo, instructions []byte) *StateMachine {
	opcodes := make([]opcodefn, len(standardopcodes)+1)
	opcodes[0] = execExtendedOpcode
	for op := range standardopcodes {
		opcodes[op] = standardopcodes[op]
	}
	sm := &StateMachine{dbl: dbl, file: dbl.FileNames[0].Path, line: 1, buf: bytes.NewBuffer(instructions), opcodes: opcodes, isStmt: dbl.Prologue.InitialIsStmt == uint8(1), address: dbl.staticBase, lastAddress: ^uint64(0)}
	return sm
}

// AllPCsForFileLines Adds all PCs for a given file and set (domain of map) of lines
// to the map value corresponding to each line.
func (lineInfo *DebugLineInfo) AllPCsForFileLines(f string, m map[int][]uint64) {
	if lineInfo == nil {
		return
	}

	var (
		lastAddr uint64
		sm       = newStateMachine(lineInfo, lineInfo.Instructions)
	)

	for {
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil {
				lineInfo.Logf("AllPCsForFileLine error: %v", err)
			}
			break
		}
		if sm.address != lastAddr && sm.isStmt && sm.valid && sm.file == f {
			if pcs, ok := m[sm.line]; ok {
				pcs = append(pcs, sm.address)
				m[sm.line] = pcs
				lastAddr = sm.address
			}
		}
	}
	return
}

var NoSourceError = errors.New("no source available")

// AllPCsBetween returns all PC addresses between begin and end (including both begin and end) that have the is_stmt flag set and do not belong to excludeFile:excludeLine
func (lineInfo *DebugLineInfo) AllPCsBetween(begin, end uint64, excludeFile string, excludeLine int) ([]uint64, error) {
	if lineInfo == nil {
		return nil, NoSourceError
	}

	var (
		pcs      []uint64
		lastaddr uint64
		sm       = newStateMachine(lineInfo, lineInfo.Instructions)
	)

	for {
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil {
				lineInfo.Logf("AllPCsBetween error: %v", err)
			}
			break
		}
		if !sm.valid {
			continue
		}
		if (sm.address > end) && (end >= sm.lastAddress) {
			break
		}
		if sm.address >= begin && sm.address <= end && sm.address > lastaddr && sm.isStmt && ((sm.file != excludeFile) || (sm.line != excludeLine)) {
			lastaddr = sm.address
			pcs = append(pcs, sm.address)
		}
	}
	return pcs, nil
}

// copy returns a copy of this state machine, running the returned state
// machine will not affect sm.
func (sm *StateMachine) copy() *StateMachine {
	var r StateMachine
	r = *sm
	r.buf = bytes.NewBuffer(sm.buf.Bytes())
	return &r
}

func (lineInfo *DebugLineInfo) stateMachineForEntry(basePC uint64) (sm *StateMachine) {
	sm = lineInfo.stateMachineCache[basePC]
	if sm == nil {
		sm = newStateMachine(lineInfo, lineInfo.Instructions)
		sm.PCToLine(basePC)
		lineInfo.stateMachineCache[basePC] = sm
	}
	sm = sm.copy()
	return
}

// PCToLine returns the filename and line number associated with pc.
// If pc isn't found inside lineInfo's table it will return the filename and
// line number associated with the closest PC address preceding pc.
// basePC will be used for caching, it's normally the entry point for the
// function containing pc.
func (lineInfo *DebugLineInfo) PCToLine(basePC, pc uint64) (string, int) {
	if lineInfo == nil {
		return "", 0
	}
	if basePC > pc {
		panic(fmt.Errorf("basePC after pc %#x %#x", basePC, pc))
	}

	sm := lineInfo.stateMachineFor(basePC, pc)

	file, line, _ := sm.PCToLine(pc)
	return file, line
}

func (lineInfo *DebugLineInfo) stateMachineFor(basePC, pc uint64) *StateMachine {
	var sm *StateMachine
	if basePC == 0 {
		sm = newStateMachine(lineInfo, lineInfo.Instructions)
	} else {
		// Try to use the last state machine that we used for this function, if
		// there isn't one or it's already past pc try to clone the cached state
		// machine stopped at the entry point of the function.
		// As a last resort start from the start of the debug_line section.
		sm = lineInfo.lastMachineCache[basePC]
		if sm == nil || sm.lastAddress >= pc {
			sm = lineInfo.stateMachineForEntry(basePC)
			lineInfo.lastMachineCache[basePC] = sm
		}
	}
	return sm
}

func (sm *StateMachine) PCToLine(pc uint64) (string, int, bool) {
	if !sm.started {
		if err := sm.next(); err != nil {
			if sm.dbl.Logf != nil {
				sm.dbl.Logf("PCToLine error: %v", err)
			}
			return "", 0, false
		}
	}
	if sm.lastAddress > pc && sm.lastAddress != ^uint64(0) {
		return "", 0, false
	}
	for {
		if sm.valid {
			if (sm.address > pc) && (pc >= sm.lastAddress) {
				return sm.lastFile, sm.lastLine, true
			}
			if sm.address == pc {
				return sm.file, sm.line, true
			}
		}
		if err := sm.next(); err != nil {
			if sm.dbl.Logf != nil {
				sm.dbl.Logf("PCToLine error: %v", err)
			}
			break
		}
	}
	if sm.valid {
		return sm.file, sm.line, true
	}
	return "", 0, false
}

// LineToPC returns the first PC address associated with filename:lineno.
func (lineInfo *DebugLineInfo) LineToPC(filename string, lineno int) uint64 {
	if lineInfo == nil {
		return 0
	}

	sm := newStateMachine(lineInfo, lineInfo.Instructions)

	// if no instruction marked is_stmt is found fallback to the first
	// instruction assigned to the filename:line.
	var fallbackPC uint64

	for {
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil && err != io.EOF {
				lineInfo.Logf("LineToPC error: %v", err)
			}
			break
		}
		if sm.line == lineno && sm.file == filename && sm.valid {
			if sm.isStmt {
				return sm.address
			} else if fallbackPC == 0 {
				fallbackPC = sm.address
			}
		}
	}
	return fallbackPC
}

// LineToPCIn returns the first PC for filename:lineno in the interval [startPC, endPC).
// This function is used to find the instruction corresponding to
// filename:lineno for a function that has been inlined.
// basePC will be used for caching, it's normally the entry point for the
// function containing pc.
func (lineInfo *DebugLineInfo) LineToPCIn(filename string, lineno int, basePC, startPC, endPC uint64) uint64 {
	if lineInfo == nil {
		return 0
	}
	if basePC > startPC {
		panic(fmt.Errorf("basePC after startPC %#x %#x", basePC, startPC))
	}

	sm := lineInfo.stateMachineFor(basePC, startPC)

	var fallbackPC uint64

	for {
		if sm.valid && sm.started {
			if sm.address >= endPC {
				break
			}
			if sm.line == lineno && sm.file == filename && sm.address >= startPC {
				if sm.isStmt {
					return sm.address
				} else {
					fallbackPC = sm.address
				}
			}
		}
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil && err != io.EOF {
				lineInfo.Logf("LineToPC error: %v", err)
			}
			break
		}

	}

	return fallbackPC
}

// PrologueEndPC returns the first PC address marked as prologue_end in the half open interval [start, end)
func (lineInfo *DebugLineInfo) PrologueEndPC(start, end uint64) (pc uint64, file string, line int, ok bool) {
	if lineInfo == nil {
		return 0, "", 0, false
	}

	sm := lineInfo.stateMachineForEntry(start)
	for {
		if sm.valid {
			if sm.address >= end {
				return 0, "", 0, false
			}
			if sm.prologueEnd {
				return sm.address, sm.file, sm.line, true
			}
		}
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil {
				lineInfo.Logf("PrologueEnd error: %v", err)
			}
			return 0, "", 0, false
		}
	}
}

// FirstStmtForLine looks in the half open interval [start, end) for the
// first PC address marked as stmt for the line at address 'start'.
func (lineInfo *DebugLineInfo) FirstStmtForLine(start, end uint64) (pc uint64, file string, line int, ok bool) {
	first := true
	sm := lineInfo.stateMachineForEntry(start)
	for {
		if sm.valid {
			if sm.address >= end {
				return 0, "", 0, false
			}
			if first {
				first = false
				file, line = sm.file, sm.line
			}
			if sm.isStmt && sm.file == file && sm.line == line {
				return sm.address, sm.file, sm.line, true
			}
		}
		if err := sm.next(); err != nil {
			if lineInfo.Logf != nil {
				lineInfo.Logf("StmtAfter error: %v", err)
			}
			return 0, "", 0, false
		}
	}
}

func (sm *StateMachine) next() error {
	sm.started = true
	if sm.valid {
		sm.lastAddress, sm.lastFile, sm.lastLine = sm.address, sm.file, sm.line

		// valid is set by either a special opcode or a DW_LNS_copy, in both cases
		// we need to reset basic_block, prologue_end and epilogue_begin
		sm.basicBlock = false
		sm.prologueEnd = false
		sm.epilogueBegin = false
	}
	if sm.endSeq {
		sm.endSeq = false
		sm.file = sm.dbl.FileNames[0].Path
		sm.line = 1
		sm.column = 0
		sm.isa = 0
		sm.isStmt = sm.dbl.Prologue.InitialIsStmt == uint8(1)
		sm.basicBlock = false
		sm.lastAddress = ^uint64(0)
	}
	b, err := sm.buf.ReadByte()
	if err != nil {
		return err
	}
	if b < sm.dbl.Prologue.OpcodeBase {
		if int(b) < len(sm.opcodes) {
			sm.valid = false
			sm.opcodes[b](sm, sm.buf)
		} else {
			// unimplemented standard opcode, read the number of arguments specified
			// in the prologue and do nothing with them
			opnum := sm.dbl.Prologue.StdOpLengths[b-1]
			for i := 0; i < int(opnum); i++ {
				util.DecodeSLEB128(sm.buf)
			}
			fmt.Printf("unknown opcode %d(0x%x), %d arguments, file %s, line %d, address 0x%x\n", b, b, opnum, sm.file, sm.line, sm.address)
		}
	} else {
		execSpecialOpcode(sm, b)
	}
	return nil
}

func execSpecialOpcode(sm *StateMachine, instr byte) {
	var (
		opcode  = uint8(instr)
		decoded = opcode - sm.dbl.Prologue.OpcodeBase
	)

	sm.lastDelta = int(sm.dbl.Prologue.LineBase + int8(decoded%sm.dbl.Prologue.LineRange))
	sm.line += sm.lastDelta
	sm.address += uint64(decoded/sm.dbl.Prologue.LineRange) * uint64(sm.dbl.Prologue.MinInstrLength)
	sm.valid = true
}

func execExtendedOpcode(sm *StateMachine, buf *bytes.Buffer) {
	_, _ = util.DecodeULEB128(buf)
	b, _ := buf.ReadByte()
	if fn, ok := extendedopcodes[b]; ok {
		fn(sm, buf)
	}
}

func copyfn(sm *StateMachine, buf *bytes.Buffer) {
	sm.valid = true
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
	if i-1 < uint64(len(sm.dbl.FileNames)) {
		sm.file = sm.dbl.FileNames[i-1].Path
	} else {
		j := (i - 1) - uint64(len(sm.dbl.FileNames))
		if j < uint64(len(sm.definedFiles)) {
			sm.file = sm.definedFiles[j].Path
		} else {
			sm.file = ""
		}
	}
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
	sm.address += uint64((255-sm.dbl.Prologue.OpcodeBase)/sm.dbl.Prologue.LineRange) * uint64(sm.dbl.Prologue.MinInstrLength)
}

func fixedadvancepc(sm *StateMachine, buf *bytes.Buffer) {
	var operand uint16
	binary.Read(buf, binary.LittleEndian, &operand)

	sm.address += uint64(operand)
}

func endsequence(sm *StateMachine, buf *bytes.Buffer) {
	sm.endSeq = true
	sm.valid = true
}

func setaddress(sm *StateMachine, buf *bytes.Buffer) {
	//TODO: this needs to be changed to support 32bit architectures (addr must be target arch pointer sized) -- also target endianness
	var addr uint64

	binary.Read(buf, binary.LittleEndian, &addr)

	sm.address = addr + sm.dbl.staticBase
}

func definefile(sm *StateMachine, buf *bytes.Buffer) {
	entry := readFileEntry(sm.dbl, sm.buf, false)
	sm.definedFiles = append(sm.definedFiles, entry)
}

func prologueend(sm *StateMachine, buf *bytes.Buffer) {
	sm.prologueEnd = true
}

func epiloguebegin(sm *StateMachine, buf *bytes.Buffer) {
	sm.epilogueBegin = true
}

func setisa(sm *StateMachine, buf *bytes.Buffer) {
	c, _ := util.DecodeULEB128(buf)
	sm.isa = c
}
