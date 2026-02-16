package main

import (
	"fmt"
	"os"

	"github.com/go-delve/delve/pkg/debugdetect"
)

func main() {
	attached, err := debugdetect.IsDebuggerAttached()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(2)
	}
	if attached {
		fmt.Println("ATTACHED")
	} else {
		fmt.Println("NOT_ATTACHED")
	}
}
