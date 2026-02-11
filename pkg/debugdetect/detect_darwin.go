package debugdetect

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// P_TRACED flag from sys/proc.h
	pTracedFlag = 0x00000800

	// sysctl constants from sys/sysctl.h
	kernProc    = 14 // KERN_PROC
	kernProcPID = 1  // KERN_PROC_PID
)

func detectDebuggerAttached() (bool, error) {
	// Use sysctl to get process info and check P_TRACED flag
	var info unix.KinfoProc

	mib := [4]int32{unix.CTL_KERN, kernProc, kernProcPID, int32(os.Getpid())}

	size := uintptr(unsafe.Sizeof(info))
	_, _, errno := unix.Syscall6(
		unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	)

	if errno != 0 {
		return false, fmt.Errorf("sysctl failed: %w", errno)
	}

	// Check if P_TRACED flag is set
	return (info.Proc.P_flag & pTracedFlag) != 0, nil
}
