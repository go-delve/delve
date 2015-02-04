package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/derekparker/delve/client/cli"
	"github.com/derekparker/delve/client/web"
)

const version string = "0.4.0.beta"

func init() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
}

func main() {
	var (
		pid     int
		run     bool
		printv  bool
		remote  bool
		address string
	)

	flag.IntVar(&pid, "pid", 0, "Pid of running process to attach to.")
	flag.BoolVar(&run, "run", false, "Compile program and begin debug session.")
	flag.BoolVar(&printv, "v", false, "Print version number and exit.")
	flag.BoolVar(&remote, "r", false, "Run in remote mode via websockets. Also useful for IDEs.")
	flag.StringVar(&address, "address", "127.0.0.1:6006", "Address to run when -r is used.")
	flag.Parse()

	if flag.NFlag() == 0 && len(flag.Args()) == 0 {
		flag.Usage()
		os.Exit(0)
	}

	if printv {
		fmt.Printf("Delve version: %s\n", version)
		os.Exit(0)
	}

	if remote {
		web.Run(run, pid, address, flag.Args())
	} else {
		cli.Run(run, pid, flag.Args())
	}

}
