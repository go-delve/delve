package proc

import (
	"errors"
	"strings"
	"testing"
)

func failIfInternalDebuggerError(t testing.TB, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "internal debugger error") {
		t.Fatalf("unexpected internal debugger error: %v", err)
	}
}

func TestFailIfInternalDebuggerError(t *testing.T) {
	t.Run("nilOK", func(t *testing.T) {
		failIfInternalDebuggerError(t, nil)
	})
	t.Run("normalErrorOK", func(t *testing.T) {
		failIfInternalDebuggerError(t, errors.New("could not find symbol"))
	})
	t.Run("internalErrorFails", func(t *testing.T) {
		rec := &recordingT{T: t}
		failIfInternalDebuggerError(rec, errors.New("internal debugger error: boom"))
		if !rec.failed {
			t.Fatal("expected failIfInternalDebuggerError to fail the test")
		}
	})
}

type recordingT struct {
	*testing.T
	failed bool
}

func (r *recordingT) Helper()                          {}
func (r *recordingT) Fatalf(format string, args ...any) { r.failed = true }
func (r *recordingT) Errorf(format string, args ...any) { r.failed = true }
func (r *recordingT) FailNow()                          { r.failed = true }
