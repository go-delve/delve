package goreadline

// sniped from https://github.com/rocaltair/goreadline

/*
#include <stdio.h>
#include <stdlib.h>
#include <readline/readline.h>
#include <readline/history.h>
#cgo LDFLAGS: -lreadline
*/
import "C"
import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

func init() {
	C.rl_catch_sigwinch = 0
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGWINCH)
	go func() {
		for sig := range c {
			switch sig {
			case syscall.SIGWINCH:
				Resize()
			default:

			}
		}
	}()
}

func Resize() {
	C.rl_resize_terminal()
}

func ReadLine(prompt *string) *string {
	var cPrompt *C.char
	if prompt != nil {
		cPrompt = C.CString(*prompt)
	}
	cLine := C.readline(cPrompt)
	if cPrompt != nil {
		C.free(unsafe.Pointer(cPrompt))
	}
	if cLine == nil {
		return nil
	}

	line := C.GoString(cLine)
	C.free(unsafe.Pointer(cLine))
	return &line
}

func AddHistory(line string) {
	cLine := C.CString(line)
	C.add_history(cLine)
	C.free(unsafe.Pointer(cLine))
}

func ClearHistory() {
	C.clear_history()
}

func WriteHistoryToFile(fileName string) int {
	cFileName := C.CString(fileName)
	err := C.write_history(cFileName)
	C.free(unsafe.Pointer(cFileName))
	return int(err)
}

func LoadHistoryFromFile(fileName string) {
	cFileName := C.CString(fileName)
	C.read_history(cFileName)
	C.free(unsafe.Pointer(cFileName))
}

func TruncateHistoryFile(fileName string, left int) {
	cFileName := C.CString(fileName)
	cLeft := C.int(left)
	C.history_truncate_file(cFileName, cLeft)
	C.free(unsafe.Pointer(cFileName))
}
