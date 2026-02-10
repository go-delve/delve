package debugdetect

/*
#include <sys/types.h>
#include <sys/sysctl.h>
#include <sys/user.h>
#include <stdlib.h>
*/
import "C"
import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"
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
	// Use kinfo_getproc to get process information
	kp, err := C.kinfo_getproc(C.int(os.Getpid()))
	if err != nil {
		return false, fmt.Errorf("kinfo_getproc failed: %v", err)
	}
	defer C.free(unsafe.Pointer(kp))

	// Check if P_TRACED flag is set in ki_flag
	// ki_flag contains the process flags including P_TRACED
	return (int(kp.ki_flag) & pTracedFlag) != 0, nil
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
