package proc

import (
	"errors"
	"strings"
	"testing"
)

func FailIfInternalDebuggerError(t testing.TB, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "internal debugger error") {
		t.Fatalf("unexpected internal debugger error: %v", err)
	}
}

func TestFailIfInternalDebuggerError(t *testing.T) {
	t.Run("nilOK", func(t *testing.T) {
		FailIfInternalDebuggerError(t, nil)
	})
	t.Run("normalErrorOK", func(t *testing.T) {
		FailIfInternalDebuggerError(t, errors.New("could not find symbol"))
	})
}
