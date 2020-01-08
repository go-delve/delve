package debug_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/debug"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestGoroutineCreationLocation(t *testing.T) {
	if runtime.GOARCH == "arm64" {
		t.Skip("arm64 does not support GetStackInfo for now")
	}
	protest.AllowRecording(t)
	withTestTarget("goroutinestackprog", t, func(tgt *debug.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(tgt, t, "main.agoroutine")
		assertNoError(tgt.Continue(), t, "Continue()")

		gs, _, err := tgt.Goroutines(0, 0)
		assertNoError(err, t, "GoroutinesInfo")

		for _, g := range gs {
			currentLocation := g.UserCurrent()
			currentFn := currentLocation.Fn
			if currentFn != nil && currentFn.BaseName() == "agoroutine" {
				createdLocation := g.Go()
				if createdLocation.Fn == nil {
					t.Fatalf("goroutine creation function is nil")
				}
				if createdLocation.Fn.BaseName() != "main" {
					t.Fatalf("goroutine creation function has wrong name: %s", createdLocation.Fn.BaseName())
				}
				if filepath.Base(createdLocation.File) != "goroutinestackprog.go" {
					t.Fatalf("goroutine creation file incorrect: %s", filepath.Base(createdLocation.File))
				}
				if createdLocation.Line != 23 {
					t.Fatalf("goroutine creation line incorrect: %v", createdLocation.Line)
				}
			}

		}

		tgt.ClearBreakpoint(bp.Addr)
		tgt.Continue()
	})
}
