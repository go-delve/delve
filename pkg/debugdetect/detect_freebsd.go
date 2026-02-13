package debugdetect

/*
#include <sys/types.h>
#include <sys/sysctl.h>
#include <sys/user.h>
#include <libutil.h>
#include <stdlib.h>
#cgo LDFLAGS: -lutil
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

const (
	// P_TRACED flag from sys/proc.h
	pTracedFlag = 0x00000800
)

func detectDebuggerAttached() (bool, error) {
	kp, err := C.kinfo_getproc(C.int(os.Getpid()))
	if err != nil {
		return false, fmt.Errorf("kinfo_getproc failed: %v", err)
	}
	defer C.free(unsafe.Pointer(kp))

	return (int(kp.ki_flag) & pTracedFlag) != 0, nil
}
