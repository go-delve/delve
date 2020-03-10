package line

import (
	"bytes"
	"compress/gzip"
	"debug/dwarf"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

func slurpGzip(path string) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	gzin, err := gzip.NewReader(fh)
	if err != nil {
		return nil, err
	}
	defer gzin.Close()
	return ioutil.ReadAll(gzin)
}

const (
	newCompileUnit = "NEW COMPILE UNIT"
	debugLineEnd   = "END"
)

func TestGrafana(t *testing.T) {
	// Compares a full execution of our state machine on the debug_line section
	// of grafana to the output generated using debug/dwarf.LineReader on the
	// same section.

	if runtime.GOOS == "windows" {
		t.Skip("filepath.Join ruins this test on windows")
	}
	debugBytes, err := slurpGzip("_testdata/debug.grafana.debug.gz")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := macho.NewFile(bytes.NewReader(debugBytes))
	if err != nil {
		t.Fatal(err)
	}

	sec := exe.Section("__debug_line")
	debugLineBytes, err := sec.Data()
	if err != nil {
		t.Fatal(err)
	}

	data, err := exe.DWARF()
	if err != nil {
		t.Fatal(err)
	}

	debugLineBuffer := bytes.NewBuffer(debugLineBytes)
	rdr := data.Reader()
	for {
		e, err := rdr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		rdr.SkipChildren()
		if e.Tag != dwarf.TagCompileUnit {
			continue
		}
		cuname, _ := e.Val(dwarf.AttrName).(string)

		lineInfo := Parse(e.Val(dwarf.AttrCompDir).(string), debugLineBuffer, t.Logf, 0, false, 8)
		sm := newStateMachine(lineInfo, lineInfo.Instructions, 8)

		lnrdr, err := data.LineReader(e)
		if err != nil {
			t.Fatal(err)
		}

		checkCompileUnit(t, cuname, lnrdr, sm)
	}
}

func checkCompileUnit(t *testing.T, cuname string, lnrdr *dwarf.LineReader, sm *StateMachine) {
	var lne dwarf.LineEntry
	for {
		if err := sm.next(); err != nil {
			if err != io.EOF {
				t.Fatalf("state machine next error: %v", err)
			}
			break
		}
		if !sm.valid {
			continue
		}

		err := lnrdr.Next(&lne)
		if err == io.EOF {
			t.Fatalf("line reader ended before our state machine for compile unit %s", cuname)
		}
		if err != nil {
			t.Fatal(err)
		}

		tgt := fmt.Sprintf("%#x %s:%d isstmt:%v prologue_end:%v epilogue_begin:%v", lne.Address, lne.File.Name, lne.Line, lne.IsStmt, lne.PrologueEnd, lne.EpilogueBegin)

		out := fmt.Sprintf("%#x %s:%d isstmt:%v prologue_end:%v epilogue_begin:%v", sm.address, sm.file, sm.line, sm.isStmt, sm.prologueEnd, sm.epilogueBegin)
		if out != tgt {
			t.Errorf("mismatch:\n")
			t.Errorf("got:\t%s\n", out)
			t.Errorf("expected:\t%s\n", tgt)
			t.Fatal("previous error")
		}
	}

	err := lnrdr.Next(&lne)
	if err != io.EOF {
		t.Fatalf("state machine ended before the line reader for compile unit %s", cuname)
	}
}

func TestMultipleSequences(t *testing.T) {
	// Check that our state machine (specifically PCToLine and AllPCsBetween)
	// are correct when dealing with units containing more than one sequence.

	const thefile = "thefile.go"

	instr := bytes.NewBuffer(nil)
	ptrSize := ptrSizeByRuntimeArch()

	write_DW_LNE_set_address := func(addr uint64) {
		instr.WriteByte(0)
		util.EncodeULEB128(instr, 9) // 1 + ptr_size
		instr.WriteByte(DW_LINE_set_address)
		util.WriteUint(instr, binary.LittleEndian, ptrSize, addr)
	}

	write_DW_LNS_copy := func() {
		instr.WriteByte(DW_LNS_copy)
	}

	write_DW_LNS_advance_pc := func(off uint64) {
		instr.WriteByte(DW_LNS_advance_pc)
		util.EncodeULEB128(instr, off)
	}

	write_DW_LNS_advance_line := func(off int64) {
		instr.WriteByte(DW_LNS_advance_line)
		util.EncodeSLEB128(instr, off)
	}

	write_DW_LNE_end_sequence := func() {
		instr.WriteByte(0)
		util.EncodeULEB128(instr, 1)
		instr.WriteByte(DW_LINE_end_sequence)
	}

	write_DW_LNE_set_address(0x400000)
	write_DW_LNS_copy() // thefile.go:1 0x400000
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:2 0x400002
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:3 0x400004
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNE_end_sequence() // thefile.go:3 ends the byte before 0x400006

	write_DW_LNE_set_address(0x600000)
	write_DW_LNS_advance_line(10)
	write_DW_LNS_copy() // thefile.go:11 0x600000
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:12 0x600002
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:13 0x600004
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNE_end_sequence() // thefile.go:13 ends the byte before 0x600006

	write_DW_LNE_set_address(0x500000)
	write_DW_LNS_advance_line(20)
	write_DW_LNS_copy() // thefile.go:21 0x500000
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:22 0x500002
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNS_advance_line(1)
	write_DW_LNS_copy() // thefile.go:23 0x500004
	write_DW_LNS_advance_pc(0x2)
	write_DW_LNE_end_sequence() // thefile.go:23 ends the byte before 0x500006

	lines := &DebugLineInfo{
		Prologue: &DebugLinePrologue{
			UnitLength:     1,
			Version:        2,
			MinInstrLength: 1,
			InitialIsStmt:  1,
			LineBase:       -3,
			LineRange:      12,
			OpcodeBase:     13,
			StdOpLengths:   []uint8{0, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0, 1},
		},
		IncludeDirs:  []string{},
		FileNames:    []*FileEntry{&FileEntry{Path: thefile}},
		Instructions: instr.Bytes(),
		ptrSize:      ptrSize,
	}

	// Test that PCToLine is correct for all three sequences

	for _, testCase := range []struct {
		pc   uint64
		line int
	}{
		{0x400000, 1},
		{0x400002, 2},
		{0x400004, 3},

		{0x500000, 21},
		{0x500002, 22},
		{0x500004, 23},

		{0x600000, 11},
		{0x600002, 12},
		{0x600004, 13},
	} {
		sm := newStateMachine(lines, lines.Instructions, lines.ptrSize)
		file, curline, ok := sm.PCToLine(testCase.pc)
		if !ok {
			t.Fatalf("Could not find %#x", testCase.pc)
		}
		if file != thefile {
			t.Fatalf("Wrong file returned for %#x %q", testCase.pc, file)
		}
		if curline != testCase.line {
			t.Errorf("Wrong line returned for %#x: got %d expected %d", testCase.pc, curline, testCase.line)
		}

	}

	// Test that AllPCsBetween is correct for all three sequences
	for _, testCase := range []struct {
		start, end uint64
		tgt        []uint64
	}{
		{0x400000, 0x400005, []uint64{0x400000, 0x400002, 0x400004}},
		{0x500000, 0x500005, []uint64{0x500000, 0x500002, 0x500004}},
		{0x600000, 0x600005, []uint64{0x600000, 0x600002, 0x600004}},
	} {
		out, err := lines.AllPCsBetween(testCase.start, testCase.end, "", -1)
		if err != nil {
			t.Fatalf("AllPCsBetween(%#x, %#x): %v", testCase.start, testCase.end, err)
		}

		if len(out) != len(testCase.tgt) {
			t.Errorf("AllPCsBetween(%#x, %#x): expected: %#x got: %#x", testCase.start, testCase.end, testCase.tgt, out)
		}
	}
}
