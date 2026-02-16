package debugdetect

import (
	"fmt"
	"runtime"
	"time"
)

// IsDebuggerAttached returns true if the current process is being debugged
// by a ptrace-based debugger (Delve, gdb, lldb, etc.).
//
// Returns an error if the debugger state cannot be determined.
// Supported platforms: linux, darwin, windows, freebsd
func IsDebuggerAttached() (bool, error) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows", "freebsd":
		return detectDebuggerAttached()
	default:
		return false, fmt.Errorf("debugger detection not supported on %s", runtime.GOOS)
	}
}

func WaitForDebugger() error {
	for {
		attached, err := IsDebuggerAttached()
		if attached || err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}
