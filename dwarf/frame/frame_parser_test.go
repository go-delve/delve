package frame_test

import (
	"testing"

	"github.com/davecheney/profile"
	"github.com/derekparker/dbg/dwarf/_helper"
	"github.com/derekparker/dbg/dwarf/frame"
)

func BenchmarkParse(b *testing.B) {
	defer profile.Start(profile.CPUProfile).Stop()
	data := dwarfhelper.GrabDebugFrameSection("../../_fixtures/testprog", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Parse(data)
	}
}
