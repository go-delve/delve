package frame_test

import (
	"testing"

	"github.com/davecheney/profile"
	"github.com/derekparker/dbg/dwarf/frame"
)

func TestParse(t *testing.T) {
	var (
		data = grabDebugFrameSection("../../_fixtures/testprog", t)
		fe   = frame.Parse(data)[0]
		ce   = fe.CIE
	)

	if ce.Length != 16 {
		t.Error("Length was not parsed correctly, got ", ce.Length)
	}

	if ce.Version != 0x3 {
		t.Fatalf("Version was not parsed correctly expected %#v got %#v", 0x3, ce.Version)
	}

	if ce.Augmentation != "" {
		t.Fatal("Augmentation was not parsed correctly")
	}

	if ce.CodeAlignmentFactor != 0x1 {
		t.Fatal("Code Alignment Factor was not parsed correctly")
	}

	if ce.DataAlignmentFactor != -4 {
		t.Fatalf("Data Alignment Factor was not parsed correctly got %#v", ce.DataAlignmentFactor)
	}

	if fe.Length != 32 {
		t.Fatal("Length was not parsed correctly, got ", fe.Length)
	}

}

func BenchmarkParse(b *testing.B) {
	defer profile.Start(profile.CPUProfile).Stop()
	data := grabDebugFrameSection("../../_fixtures/testprog", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Parse(data)
	}
}
