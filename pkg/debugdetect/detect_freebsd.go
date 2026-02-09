package debugdetect

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// P_TRACED flag from sys/proc.h
	pTracedFlag = 0x00000800
)

func detectDebuggerAttached() (bool, error) {
	// Try sysctl first (more reliable)
	attached, err := detectFreeBSDSysctl()
	if err == nil {
		return attached, nil
	}

	// Fall back to procfs if available
	attachedProcfs, errProcfs := detectFreeBSDProcfs()
	if errProcfs == nil {
		return attachedProcfs, nil
	}

	// Both methods failed
	return false, fmt.Errorf("sysctl failed: %v; procfs fallback failed: %v", err, errProcfs)
}

func detectFreeBSDSysctl() (bool, error) {
	// Use sysctl similar to Darwin
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

	// Check if P_TRACED flag is set (ki_flag on FreeBSD)
	return (info.Proc.P_flag & pTracedFlag) != 0, nil
}

func detectFreeBSDProcfs() (bool, error) {
	// Try reading /proc/curproc/status (similar to Linux)
	f, err := os.Open("/proc/curproc/status")
	if err != nil {
		return false, fmt.Errorf("failed to read /proc/curproc/status: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "TracerPid:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return false, fmt.Errorf("malformed TracerPid line: %s", line)
			}
			pid, err := strconv.Atoi(fields[1])
			if err != nil {
				return false, fmt.Errorf("failed to parse TracerPid: %w", err)
			}
			return pid != 0, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("error reading /proc/curproc/status: %w", err)
	}

	return false, fmt.Errorf("TracerPid not found in /proc/curproc/status")
}
