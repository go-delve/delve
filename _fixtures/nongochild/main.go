package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	file := "child.sh"
	if runtime.GOOS == "windows" {
		// This path is not actually exercised currently because prog_windows.go
		// doesn't handle ErrBadBinaryInfo.
		file = "child.bat"
	}
	file, err := filepath.Abs(file)
	if err != nil {
		panic(err)
	}

	output, err := exec.Command(file).Output()
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(output)
}
