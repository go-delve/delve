package main

import (
	"fmt"
	"os"

	"github.com/go-delve/delve/pkg/debugdetect"
)

func main() {
	err := debugdetect.WaitForDebugger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(2)
	}
	fmt.Println("DEBUGGER_FOUND")
}
