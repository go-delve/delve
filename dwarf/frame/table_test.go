package frame_test

import (
	"path/filepath"
	"testing"

	"github.com/derekparker/delve/helper"
	"github.com/derekparker/delve/proctl"
)

func TestFindReturnAddress(t *testing.T) {
	var testfile, _ = filepath.Abs("../../_fixtures/testnextprog")

	helper.WithTestProcess(testfile, t, func(p *proctl.DebuggedProcess) {
		var (
			fdes = p.FrameEntries
			gsd  = p.GoSymTable
		)

		testsourcefile := testfile + ".go"
		start, _, err := gsd.LineToPC(testsourcefile, 24)
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

		fde, err := fdes.FDEForPC(start)
		if err != nil {
			t.Fatal(err)
		}

		ret := fde.ReturnAddressOffset(start)
		if err != nil {
			t.Fatal(err)
		}

		addr := p.CurrentThread.ReturnAddressFromOffset(ret)

		expected := uint64(0x400ed3)
		if addr != expected {
			t.Fatalf("return address not found correctly, expected %#v got %#v", expected, addr)
		}
	})
}
