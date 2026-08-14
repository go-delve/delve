package proc

import (
	"strings"
	"testing"
)

// FailIfInternalDebuggerError fails t if err is an internal debugger error.
// Visible to proc_test (see variables_fuzz_test.go).
func FailIfInternalDebuggerError(t testing.TB, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "internal debugger error") {
		t.Fatalf("unexpected internal debugger error: %v", err)
	}
}
