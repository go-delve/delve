package proc_test

import (
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestStacktraceExtlinkMac(t *testing.T) {
	// Tests stacktrace for programs built using external linker.
	// See issue #3194
	skipOn(t, "broken on darwin/amd64/pie", "darwin", "amd64", "pie")
	withTestProcess("issue3194", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(grp.Continue(), t, "First Continue()")
		frames, err := proc.ThreadStacktrace(p, p.CurrentThread(), 10)
		assertNoError(err, t, "ThreadStacktrace")
		logStacktrace(t, p, frames)
		if len(frames) < 2 || frames[0].Call.Fn.Name != "main.main" || frames[1].Call.Fn.Name != "runtime.main" {
			t.Fatalf("bad stacktrace")
		}
	})
}

func TestRefreshCurThreadSelGAfterContinueOnceError(t *testing.T) {
	// Issue #2078:
	// Tests that on macOS/lldb the current thread/selected goroutine are
	// refreshed after ContinueOnce returns an error due to a segmentation
	// fault.

	skipUnlessOn(t, "N/A", "lldb")

	withTestProcess("issue2078", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFileBreakpoint(p, t, fixture.Source, 4)
		assertNoError(grp.Continue(), t, "Continue() (first)")
		if grp.Continue() == nil {
			pc := currentPC(p, t)
			f, l, fn := p.BinInfo().PCToLine(pc)
			t.Logf("Second continue did not return an error %s:%d %#v", f, l, fn)
			if fn != nil && fn.Name == "runtime.fatalpanic" {
				// this is also ok, it just means this debugserver supports --unmask-signals and it's working as intended.
				return
			}
		}
		g := p.SelectedGoroutine()
		if g.CurrentLoc.Line != 9 {
			t.Fatalf("wrong current location %s:%d (expected :9)", g.CurrentLoc.File, g.CurrentLoc.Line)
		}
	})
}
