package terminal

import (
	"errors"
	"net/rpc"
	"testing"
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
