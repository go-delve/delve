package native

import (
	"errors"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
)

func TestAddInitialTargetFailure(t *testing.T) {
	wantErr := errors.New("could not load executable")
	group := &processGroup{
		addTarget: func(proc.ProcessInternal, int, proc.Thread, string, proc.StopReason, string) (*proc.Target, error) {
			return nil, wantErr
		},
	}
	// The caller owns cleanup of the initial process. Adding it must not
	// invoke child-process detach or require an initialized ptrace thread.
	process := &nativeProcess{pid: 1234}
	_, err := group.add(process, process.pid, nil, "missing", proc.StopAttached, "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("add returned %v, want %v", err, wantErr)
	}
}
