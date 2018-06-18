package line

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/pkg/profile"
)

var userTestFile string

func TestMain(m *testing.M) {
	flag.StringVar(&userTestFile, "user", "", "runs line parsing test on one extra file")
	flag.Parse()
	os.Exit(m.Run())
}

func grabDebugLineSection(p string, t *testing.T) []byte {
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ef, err := elf.NewFile(f)
	if err == nil {
		data, _ := godwarf.GetDebugSectionElf(ef, "line")
		return data
	}

	pf, err := pe.NewFile(f)
	if err == nil {
		data, _ := godwarf.GetDebugSectionPE(pf, "line")
		return data
	}

	mf, err := macho.NewFile(f)
	if err == nil {
		data, _ := godwarf.GetDebugSectionMacho(mf, "line")
		return data
	}

	return nil
}

const (
	lineBaseGo14    int8   = -1
	lineBaseGo18    int8   = -4
	lineRangeGo14   uint8  = 4
	lineRangeGo18   uint8  = 10
	versionGo14     uint16 = 2
	versionGo111    uint16 = 3
	opcodeBaseGo14  uint8  = 10
	opcodeBaseGo111 uint8  = 11
)

func testDebugLinePrologueParser(p string, t *testing.T) {
	data := grabDebugLineSection(p, t)
	debugLines := ParseAll(data, nil)

	mainFileFound := false

	for _, dbl := range debugLines {
		prologue := dbl.Prologue

		if prologue.Version != versionGo14 && prologue.Version != versionGo111 {
			t.Fatal("Version not parsed correctly", prologue.Version)
		}

		if prologue.MinInstrLength != uint8(1) {
			t.Fatal("Minimum Instruction Length not parsed correctly", prologue.MinInstrLength)
		}

		if prologue.InitialIsStmt != uint8(1) {
			t.Fatal("Initial value of 'is_stmt' not parsed correctly", prologue.InitialIsStmt)
		}

		if prologue.LineBase != lineBaseGo14 && prologue.LineBase != lineBaseGo18 {
			// go < 1.8 uses -1
			// go >= 1.8 uses -4
			t.Fatal("Line base not parsed correctly", prologue.LineBase)
		}

		if prologue.LineRange != lineRangeGo14 && prologue.LineRange != lineRangeGo18 {
			// go < 1.8 uses 4
			// go >= 1.8 uses 10
			t.Fatal("Line Range not parsed correctly", prologue.LineRange)
		}

		if prologue.OpcodeBase != opcodeBaseGo14 && prologue.OpcodeBase != opcodeBaseGo111 {
			t.Fatal("Opcode Base not parsed correctly", prologue.OpcodeBase)
		}

		lengths := []uint8{0, 1, 1, 1, 1, 0, 0, 0, 1, 0}
		for i, l := range prologue.StdOpLengths {
			if l != lengths[i] {
				t.Fatal("Length not parsed correctly", l)
			}
		}

		if len(dbl.IncludeDirs) != 0 {
			t.Fatal("Include dirs not parsed correctly")
		}

		for _, n := range dbl.FileNames {
			if strings.Contains(n.Path, "/delve/_fixtures/testnextprog.go") {
				mainFileFound = true
				break
			}
		}
	}
	if !mainFileFound {
		t.Fatal("File names table not parsed correctly")
	}
}

func TestUserFile(t *testing.T) {
	if userTestFile == "" {
		return
	}
	t.Logf("testing %q", userTestFile)
	testDebugLinePrologueParser(userTestFile, t)
}

func TestDebugLinePrologueParser(t *testing.T) {
	// Test against known good values, from readelf --debug-dump=rawline _fixtures/testnextprog
	p, err := filepath.Abs("../../../_fixtures/testnextprog")
	if err != nil {
		t.Fatal(err)
	}

	err = exec.Command("go", "build", "-gcflags=-N -l", "-o", p, p+".go").Run()
	if err != nil {
		t.Fatal("Could not compile test file", p, err)
	}
	defer os.Remove(p)
	testDebugLinePrologueParser(p, t)
}

func BenchmarkLineParser(b *testing.B) {
	defer profile.Start(profile.MemProfile).Stop()
	p, err := filepath.Abs("../../../_fixtures/testnextprog")
	if err != nil {
		b.Fatal(err)
	}
	err = exec.Command("go", "build", "-gcflags=-N -l", "-o", p, p+".go").Run()
	if err != nil {
		b.Fatal("Could not compile test file", p, err)
	}
	defer os.Remove(p)

	data := grabDebugLineSection(p, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ParseAll(data, nil)
	}
}

func loadBenchmarkData(tb testing.TB) DebugLines {
	p, err := filepath.Abs("../../../_fixtures/debug_line_benchmark_data")
	if err != nil {
		tb.Fatal("Could not find test data", p, err)
	}

	data, err := ioutil.ReadFile(p)
	if err != nil {
		tb.Fatal("Could not read test data", err)
	}

	return ParseAll(data, nil)
}

func BenchmarkStateMachine(b *testing.B) {
	lineInfos := loadBenchmarkData(b)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sm := newStateMachine(lineInfos[0], lineInfos[0].Instructions)

		for {
			if err := sm.next(); err != nil {
				break
			}
		}
	}
}

type pctolineEntry struct {
	pc   uint64
	file string
	line int
}

func (entry *pctolineEntry) match(file string, line int) bool {
	if entry.file == "" {
		return true
	}
	return entry.file == file && entry.line == line
}

func setupTestPCToLine(t testing.TB, lineInfos DebugLines) []pctolineEntry {
	entries := []pctolineEntry{}

	sm := newStateMachine(lineInfos[0], lineInfos[0].Instructions)
	for {
		if err := sm.next(); err != nil {
			break
		}
		if sm.valid {
			if len(entries) == 0 || entries[len(entries)-1].pc != sm.address {
				entries = append(entries, pctolineEntry{pc: sm.address, file: sm.file, line: sm.line})
			} else if len(entries) > 0 {
				// having two entries at the same PC address messes up the test
				entries[len(entries)-1].file = ""
			}
		}
	}

	for i := 1; i < len(entries); i++ {
		if entries[i].pc <= entries[i-1].pc {
			t.Fatalf("not monotonically increasing %d %x", i, entries[i].pc)
		}
	}

	return entries
}

func runTestPCToLine(t testing.TB, lineInfos DebugLines, entries []pctolineEntry, log bool, testSize uint64) {
	const samples = 1000
	t0 := time.Now()

	i := 0
	for pc := entries[0].pc; pc <= entries[0].pc+testSize; pc++ {
		file, line := lineInfos[0].PCToLine(pc/0x1000*0x1000, pc)
		if pc == entries[i].pc {
			if i%samples == 0 && log {
				fmt.Printf("match %x / %x (%v)\n", pc, entries[len(entries)-1].pc, time.Since(t0)/samples)
				t0 = time.Now()
			}

			if !entries[i].match(file, line) {
				t.Fatalf("Mismatch at PC %#x, expected %s:%d got %s:%d", pc, entries[i].file, entries[i].line, file, line)
			}
			i++
		} else {
			if !entries[i-1].match(file, line) {
				t.Fatalf("Mismatch at PC %#x, expected %s:%d (from previous valid entry) got %s:%d", pc, entries[i-1].file, entries[i-1].line, file, line)
			}
		}
	}
}

func TestPCToLine(t *testing.T) {
	lineInfos := loadBenchmarkData(t)

	entries := setupTestPCToLine(t, lineInfos)
	runTestPCToLine(t, lineInfos, entries, true, 0x50000)
	t.Logf("restart form beginning")
	runTestPCToLine(t, lineInfos, entries, true, 0x10000)
}

func BenchmarkPCToLine(b *testing.B) {
	lineInfos := loadBenchmarkData(b)

	entries := setupTestPCToLine(b, lineInfos)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runTestPCToLine(b, lineInfos, entries, false, 0x10000)
	}
}
