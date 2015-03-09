package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/derekparker/delve/client/cli"
)

const version string = "0.5.0.beta"

var usage string = fmt.Sprintf(`Delve version %s

flags:
  -v - Print version

Invoke with the path to a binary:

dlv ./path/to/prog

or use the following commands:
  run - Build, run, and attach to program
  attach - Attach to running process
`, version)

func init() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
}

func main() {
	var printv bool

	flag.Parse()

	if flag.NFlag() == 0 && len(flag.Args()) == 0 {
		fmt.Println(usage)
		os.Exit(0)
	}

	if printv {
		fmt.Printf("Delve version: %s\n", version)
		os.Exit(0)
	}

	cli.Run(os.Args[1:])
}
