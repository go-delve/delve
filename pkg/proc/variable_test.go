package proc_test

import (
	"path/filepath"
	"testing"

	"github.com/derekparker/delve/pkg/proc"
	protest "github.com/derekparker/delve/pkg/proc/test"
)

func TestGoroutineCreationLocation(t *testing.T) {
	protest.AllowRecording(t)
	withTestProcess("goroutinestackprog", t, func(p proc.Process, fixture protest.Fixture) {
		bp, err := setFunctionBreakpoint(p, "main.agoroutine")
		assertNoError(err, t, "BreakByLocation()")
		assertNoError(proc.Continue(p), t, "Continue()")

		gs, err := proc.GoroutinesInfo(p)
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
				if createdLocation.Line != 20 {
					t.Fatalf("goroutine creation line incorrect: %v", createdLocation.Line)
				}
			}

		}

		p.ClearBreakpoint(bp.Addr)
		proc.Continue(p)
	})
}
