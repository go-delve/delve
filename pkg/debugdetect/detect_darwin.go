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
)

func detectDebuggerAttached() (bool, error) {
	// Use sysctl to get process info and check P_TRACED flag
	var info unix.KinfoProc

	mib := [4]int32{unix.CTL_KERN, unix.KERN_PROC, unix.KERN_PROC_PID, int32(os.Getpid())}

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
