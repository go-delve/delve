package proc

import "testing"

// FailIfInternalDebuggerError exports failIfInternalDebuggerError for proc_test
// fuzz tests.
func FailIfInternalDebuggerError(t testing.TB, err error) {
	failIfInternalDebuggerError(t, err)
}
