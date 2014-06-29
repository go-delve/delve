package frame_test

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/derekparker/dbg/_helper"
	"github.com/derekparker/dbg/dwarf/_helper"
	"github.com/derekparker/dbg/dwarf/frame"
	"github.com/derekparker/dbg/proctl"
)

var testfile string

func init() {
	testfile, _ = filepath.Abs("../../_fixtures/testprog")
}

func grabDebugFrameSection(fp string, t *testing.T) []byte {
	p, err := filepath.Abs(fp)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}

	ef, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}

	data, err := ef.Section(".debug_frame").Data()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestFindReturnAddress(t *testing.T) {
	var (
		dbframe = grabDebugFrameSection(testfile, t)
		fdes    = frame.Parse(dbframe)
		gsd     = dwarfhelper.GosymData(testfile, t)
	)

	helper.WithTestProcess("../../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		testsourcefile := testfile + ".go"
		start, _, err := gsd.LineToPC(testsourcefile, 9)
		if err != nil {
			t.Fatal(err)
		}

		_, err = p.Break(uintptr(start))
		if err != nil {
			t.Fatal(err)
		}

		err = p.Continue()
		if err != nil {
			t.Fatal(err)
		}

		regs, err := p.Registers()
		if err != nil {
			t.Fatal(err)
		}

		fde, err := fdes.FDEForPC(start)
		if err != nil {
			t.Fatal(err)
		}

		end, _, err := gsd.LineToPC(testsourcefile, 19)
		if err != nil {
			t.Fatal(err)
		}

		ret := fde.ReturnAddressOffset(start)
		if err != nil {
			t.Fatal(err)
		}

		addr := uint64(int64(regs.Rsp) + ret)
		data := make([]byte, 8)

		syscall.PtracePeekText(p.Pid, uintptr(addr), data)
		addr = binary.LittleEndian.Uint64(data)

		if addr != end {
			t.Fatalf("return address not found correctly, expected %#v got %#v", end, ret)
		}
	})
}
