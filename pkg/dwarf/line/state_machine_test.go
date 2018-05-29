package line

import (
	"bytes"
	"compress/gzip"
	"debug/dwarf"
	"debug/macho"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"testing"
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

		lineInfo := Parse(e.Val(dwarf.AttrCompDir).(string), debugLineBuffer, t.Logf, 0)
		sm := newStateMachine(lineInfo, lineInfo.Instructions)

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
