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
}
