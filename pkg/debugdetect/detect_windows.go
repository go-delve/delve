package debugdetect

import (
	"golang.org/x/sys/windows"
)

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procIsDebuggerPresent = kernel32.NewProc("IsDebuggerPresent")
)

func detectDebuggerAttached() (bool, error) {
	// Use IsDebuggerPresent from kernel32.dll
	// This checks the BeingDebugged flag in the PEB
	flag, _, _ := procIsDebuggerPresent.Call()
	return flag != 0, nil
}
