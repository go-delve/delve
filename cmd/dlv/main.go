package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/derekparker/delve/client/cli"
)

const version string = "0.5.0.beta"

var usage string = `Delve version %s

flags:
%s

Invoke with the path to a binary:

  dlv ./path/to/prog

or use the following commands:
  run - Build, run, and attach to program
  test - Build test binary, run and attach to it
  attach - Attach to running process
`

func init() {
	flag.Usage = help

	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
}

func main() {
	var printv, printhelp bool

	flag.BoolVar(&printv, "v", false, "Print version number and exit.")
	flag.BoolVar(&printhelp, "h", false, "Print help text and exit.")
	flag.Parse()

	if flag.NFlag() == 0 && len(flag.Args()) == 0 {
		help()
		os.Exit(0)
	}

	if printv {
		fmt.Printf("Delve version: %s\n", version)
		os.Exit(0)
	}

	if printhelp {
		help()
		os.Exit(0)
	}

	cli.Run(os.Args[1:])
}

// help prints help text to os.Stderr.
func help() {
	flags := ""
	flag.VisitAll(func(f *flag.Flag) {
		doc := fmt.Sprintf("  -%s=%s: %s\n", f.Name, f.DefValue, f.Usage)
		flags += doc
	})
	fmt.Fprintf(os.Stderr, usage, version, flags)
}
