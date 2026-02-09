package debugdetect

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func detectDebuggerAttached() (bool, error) {
	// Use IsDebuggerPresent from kernel32.dll
	// This checks the BeingDebugged flag in the PEB
	ret, err := windows.IsDebuggerPresent()
	if err != nil {
		return false, fmt.Errorf("IsDebuggerPresent failed: %w", err)
	}
	return ret, nil
}
