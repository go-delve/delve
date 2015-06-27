package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/rpc"
	"github.com/derekparker/delve/terminal"
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
}

func main() {
	var printv, printhelp bool
	var addr string
	var logEnabled bool
	var headless bool

	flag.BoolVar(&printv, "version", false, "Print version number and exit.")
	flag.StringVar(&addr, "addr", "localhost:0", "Debugging server listen address.")
	flag.BoolVar(&logEnabled, "log", false, "Enable debugging server logging.")
	flag.BoolVar(&headless, "headless", false, "Run in headless mode.")
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

	defer func() {
		if err := recover(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	status := run(addr, logEnabled, headless)
	fmt.Println("[Hope I was of service hunting your bug!]")
	os.Exit(status)
}

func run(addr string, logEnabled, headless bool) int {
	// Collect launch arguments
	var processArgs []string
	var attachPid int
	switch flag.Args()[0] {
	case "run":
		const debugname = "debug"
		cmd := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
		err := cmd.Run()
		if err != nil {
			fmt.Println("Could not compile program:", err)
			return 1
		}
		fp, err := filepath.Abs("./" + debugname)
		if err != nil {
			fmt.Println(err)
			return 1
		}
		defer os.Remove(fp)

		processArgs = append([]string{"./" + debugname}, flag.Args()[1:]...)
	case "test":
		wd, err := os.Getwd()
		if err != nil {
			fmt.Println(err)
			return 1
		}
		base := filepath.Base(wd)
		cmd := exec.Command("go", "test", "-c", "-gcflags", "-N -l")
		err = cmd.Run()
		if err != nil {
			fmt.Errorf("Could not compile program: %s\n", err)
			return 1
		}
		debugname := "./" + base + ".test"
		defer os.Remove(debugname)

		processArgs = append([]string{debugname}, flag.Args()[1:]...)
	case "attach":
		pid, err := strconv.Atoi(flag.Args()[1])
		if err != nil {
			fmt.Errorf("Invalid pid: %d", flag.Args()[1])
			return 1
		}
		attachPid = pid
	default:
		processArgs = flag.Args()
	}

	// Make a TCP listener
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("couldn't start listener: %s\n", err)
		return 1
	}
	defer listener.Close()

	// Create and start a debugger server
	var server service.Server
	server = rpc.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: processArgs,
		AttachPid:   attachPid,
	}, logEnabled)
	if err := server.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var status int
	if !headless {
		// Create and start a terminal
		var client service.Client
		client = rpc.NewClient(listener.Addr().String())
		term := terminal.New(client)
		err, status = term.Run()
	} else {
		ch := make(chan os.Signal)
		signal.Notify(ch, sys.SIGINT)
		<-ch
		err = server.Stop(true)
	}

	if err != nil {
		fmt.Println(err)
	}

	return status
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
