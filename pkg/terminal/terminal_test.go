package terminal

import (
	"errors"
	"net/rpc"
	"testing"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/liner"
)

func TestIsErrProcessExited(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		result bool
	}{
		{"empty error", errors.New(""), false},
		{"non-ServerError", errors.New("Process 33122 has exited with status 0"), false},
		{"ServerError with zero status", rpc.ServerError("Process 33122 has exited with status 0"), true},
		{"ServerError with non-zero status", rpc.ServerError("Process 2 has exited with status 25"), true},
	}
	for _, test := range tests {
		if isErrProcessExited(test.err) != test.result {
			t.Error(test.name)
		}
	}
}

// fakeExitClient implements just the service.Client methods that handleExit
// needs; the rest are nil and must not be called by the test.
type fakeExitClient struct {
	service.Client
	multiclient bool
	attached    bool
	detachKill  *bool
}

func (c *fakeExitClient) GetState() (*api.DebuggerState, error) { return &api.DebuggerState{}, nil }
func (c *fakeExitClient) IsMulticlient() bool                   { return c.multiclient }
func (c *fakeExitClient) AttachedToExistingProcess() bool       { return c.attached }
func (c *fakeExitClient) Detach(kill bool) error                { c.detachKill = &kill; return nil }

func TestExitDetachWithoutKill(t *testing.T) {
	oldYesNo := yesno
	defer func() { yesno = oldYesNo }()
	yesnoCalled := false
	yesno = func(_ *liner.State, _, _ string) (bool, error) {
		yesnoCalled = true
		return true, nil // the interactive default kills the process
	}

	fc := &fakeExitClient{attached: true}
	term := &Term{client: fc}

	// "exit -d" records the intent to detach without killing.
	if err := exitCommand(term, callContext{}, "-d"); err == nil {
		t.Fatal("exitCommand should return an ExitRequestError")
	}

	status, err := term.handleExit()
	if err != nil {
		t.Fatalf("handleExit returned error: %v", err)
	}
	if status != 0 {
		t.Errorf("exit status = %d, want 0", status)
	}
	if yesnoCalled {
		t.Error("exit -d must not prompt whether to kill the process")
	}
	if fc.detachKill == nil {
		t.Fatal("Detach was not called")
	}
	if *fc.detachKill {
		t.Error("exit -d must detach without killing the process (kill=false)")
	}
}
