package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/derekparker/delve/config"
	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
	"github.com/derekparker/delve/terminal"

	"github.com/spf13/cobra"
)

const version string = "0.11.0-alpha"

// Build is the current git hash.
var Build string

var (
	// Log is whether to log debug statements.
	Log bool
	// Headless is whether to run without terminal.
	Headless bool
	// Addr is the debugging server listen address.
	Addr string
	// InitFile is the path to initialization file.
	InitFile string
	// BuildFlags is the flags passed during compiler invocation.
	BuildFlags string

	traceAttachPid  int
	traceStackDepth int

	conf *config.Config

	rootCommand *cobra.Command
)

const dlvCommandLongDesc = `Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.
`

func init() {
	buildFlagsDefault := ""
	if runtime.GOOS == "windows" {
		// Work-around for https://github.com/golang/go/issues/13154
		buildFlagsDefault = "-ldflags=-linkmode internal"
	}

	// Main dlv root command.
	rootCommand = &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long:  dlvCommandLongDesc,
	}

	rootCommand.PersistentFlags().StringVarP(&Addr, "listen", "l", "localhost:0", "Debugging server listen address.")
	rootCommand.PersistentFlags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	rootCommand.PersistentFlags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")
	rootCommand.PersistentFlags().StringVar(&InitFile, "init", "", "Init file, executed by the terminal client.")
	rootCommand.PersistentFlags().StringVar(&BuildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler.")

	// 'version' subcommand.
	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Delve Debugger\nVersion: %s\nBuild: %s\n", version, Build)
		},
	}
	rootCommand.AddCommand(versionCommand)

	// Deprecated 'run' subcommand.
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Deprecated command. Use 'debug' instead.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("This command is deprecated, please use 'debug' instead.")
			os.Exit(0)
		},
	}
	rootCommand.AddCommand(runCommand)

	// 'debug' subcommand.
	debugCommand := &cobra.Command{
		Use:   "debug",
		Short: "Compile and begin debugging program.",
		Long: `Compiles your program with optimizations disabled,
starts and attaches to it, and enables you to immediately begin debugging your program.`,
		Run: debugCmd,
	}
	rootCommand.AddCommand(debugCommand)

	// 'exec' subcommand.
	execCommand := &cobra.Command{
		Use:   "exec [./path/to/binary]",
		Short: "Runs precompiled binary, attaches and begins debug session.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a path to a binary")
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(execute(0, args, conf))
		},
	}
	rootCommand.AddCommand(execCommand)

	// 'trace' subcommand.
	traceCommand := &cobra.Command{
		Use:   "trace [regexp]",
		Short: "Compile and begin tracing program.",
		Long:  "Trace program execution. Will set a tracepoint on every function matching [regexp] and output information when tracepoint is hit.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a function to trace")
			}
			return nil
		},
		Run: traceCmd,
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	traceCommand.Flags().IntVarP(&traceStackDepth, "stack", "s", 0, "Show stack trace with given depth.")
	rootCommand.AddCommand(traceCommand)

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test",
		Short: "Compile test binary and begin debugging program.",
		Long:  `Compiles a test binary with optimizations disabled, starts and attaches to it, and enable you to immediately begin debugging your program.`,
		Run:   testCmd,
	}
	rootCommand.AddCommand(testCommand)

	// 'attach' subcommand.
	attachCommand := &cobra.Command{
		Use:   "attach [pid]",
		Short: "Attach to running process and begin debugging.",
		Long:  "Attach to running process and begin debugging.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a PID")
			}
			return nil
		},
		Run: attachCmd,
	}
	rootCommand.AddCommand(attachCommand)

	// 'connect' subcommand.
	connectCommand := &cobra.Command{
		Use:   "connect [addr]",
		Short: "Connect to a headless debug server.",
		Long:  "Connect to a headless debug server.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide an address as the first argument")
			}
			return nil
		},
		Run: connectCmd,
	}
	rootCommand.AddCommand(connectCommand)

}

func main() {
	// Config setup and load.
	conf = config.LoadConfig()

	rootCommand.Execute()
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		const debugname = "debug"
		err := gobuild(debugname)
		if err != nil {
			return 1
		}
		fp, err := filepath.Abs("./" + debugname)
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			return 1
		}
		defer os.Remove(fp)

		processArgs := append([]string{"./" + debugname}, args...)
		return execute(0, processArgs, conf)
	}()
	os.Exit(status)
}

func traceCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		const debugname = "debug"
		var processArgs []string
		if traceAttachPid == 0 {
			if err := gobuild(debugname); err != nil {
				return 1
			}
			fp, err := filepath.Abs("./" + debugname)
			if err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				return 1
			}
			defer os.Remove(fp)

			processArgs = append([]string{"./" + debugname}, args...)
		}
		// Make a TCP listener
		listener, err := net.Listen("tcp", Addr)
		if err != nil {
			fmt.Printf("couldn't start listener: %s\n", err)
			return 1
		}
		defer listener.Close()

		// Create and start a debug server
		server := rpc.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: processArgs,
			AttachPid:   traceAttachPid,
		}, Log)
		if err := server.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		client := rpc.NewClient(listener.Addr().String())
		funcs, err := client.ListFunctions(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for i := range funcs {
			_, err := client.CreateBreakpoint(&api.Breakpoint{FunctionName: funcs[i], Tracepoint: true, Line: -1, Stacktrace: traceStackDepth})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		cmds := terminal.DebugCommands(client)
		cmd := cmds.Find("continue")
		t := terminal.New(client, nil)
		defer t.Close()
		err = cmd(t, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}()
	os.Exit(status)
}

func testCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			return 1
		}
		base := filepath.Base(wd)
		err = gotestbuild()
		if err != nil {
			return 1
		}
		debugname := "./" + base + ".test"
		// On Windows, "go test" generates an executable with the ".exe" extension
		if runtime.GOOS == "windows" {
			debugname += ".exe"
		}
		defer os.Remove(debugname)
		processArgs := append([]string{debugname}, args...)

		return execute(0, processArgs, conf)
	}()
	os.Exit(status)
}

func attachCmd(cmd *cobra.Command, args []string) {
	pid, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid pid: %s\n", args[0])
		os.Exit(1)
	}
	os.Exit(execute(pid, nil, conf))
}

func connectCmd(cmd *cobra.Command, args []string) {
	addr := args[0]
	if addr == "" {
		fmt.Fprintf(os.Stderr, "An empty address was provided. You must provide an address as the first argument.\n")
		os.Exit(1)
	}
	os.Exit(connect(addr, conf))
}

func connect(addr string, conf *config.Config) int {
	// Create and start a terminal - attach to running instance
	var client service.Client
	client = rpc.NewClient(addr)
	term := terminal.New(client, conf)
	status, err := term.Run()
	if err != nil {
		fmt.Println(err)
	}
	return status
}

func execute(attachPid int, processArgs []string, conf *config.Config) int {
	// Make a TCP listener
	listener, err := net.Listen("tcp", Addr)
	if err != nil {
		fmt.Printf("couldn't start listener: %s\n", err)
		return 1
	}
	defer listener.Close()

	if Headless && (InitFile != "") {
		fmt.Fprintf(os.Stderr, "Warning: init file ignored\n")
	}

	// Create and start a debugger server
	server := rpc.NewServer(&service.Config{
		Listener:    listener,
		ProcessArgs: processArgs,
		AttachPid:   attachPid,
	}, Log)
	if err := server.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var status int
	if Headless {
		ch := make(chan os.Signal)
		signal.Notify(ch, syscall.SIGINT)
		<-ch
		err = server.Stop(true)
	} else {
		// Create and start a terminal
		var client service.Client
		client = rpc.NewClient(listener.Addr().String())
		term := terminal.New(client, conf)
		term.InitFile = InitFile
		status, err = term.Run()
	}

	if err != nil {
		fmt.Println(err)
	}

	return status
}

func gobuild(debugname string) error {
	return gocommand("build", "-o", debugname, BuildFlags)
}

func gotestbuild() error {
	return gocommand("test", "-c", BuildFlags)
}

func gocommand(command string, args ...string) error {
	allargs := []string{command, "-gcflags", "-N -l"}
	allargs = append(allargs, args...)
	goBuild := exec.Command("go", allargs...)
	goBuild.Stderr = os.Stderr
	return goBuild.Run()
}
