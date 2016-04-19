package cmds

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
	"github.com/derekparker/delve/version"
	"github.com/spf13/cobra"
)

var (
	// Log is whether to log debug statements.
	Log bool
	// Headless is whether to run without terminal.
	Headless bool
	// AcceptMulti allows multiple clients to connect to the same server
	AcceptMulti bool
	// Addr is the debugging server listen address.
	Addr string
	// InitFile is the path to initialization file.
	InitFile string
	// BuildFlags is the flags passed during compiler invocation.
	BuildFlags string

	// RootCommand is the root of the command tree.
	RootCommand *cobra.Command

	traceAttachPid  int
	traceStackDepth int

	conf *config.Config
)

const (
	debugname     = "debug"
	testdebugname = "debug.test"
)

const dlvCommandLongDesc = `Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.
`

// New returns an initialized command tree.
func New() *cobra.Command {
	// Config setup and load.
	conf = config.LoadConfig()
	buildFlagsDefault := ""
	if runtime.GOOS == "windows" {
		// Work-around for https://github.com/golang/go/issues/13154
		buildFlagsDefault = "-ldflags=-linkmode internal"
	}

	// Main dlv root command.
	RootCommand = &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long:  dlvCommandLongDesc,
	}

	RootCommand.PersistentFlags().StringVarP(&Addr, "listen", "l", "localhost:0", "Debugging server listen address.")
	RootCommand.PersistentFlags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	RootCommand.PersistentFlags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")
	RootCommand.PersistentFlags().BoolVarP(&AcceptMulti, "accept-multiclient", "", false, "Allows a headless server to accept multiple client connection. Note that the server API is not reentrant and clients will have to coordinate")
	RootCommand.PersistentFlags().StringVar(&InitFile, "init", "", "Init file, executed by the terminal client.")
	RootCommand.PersistentFlags().StringVar(&BuildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler.")

	// 'version' subcommand.
	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Delve Debugger\n%s\n", version.DelveVersion)
		},
	}
	RootCommand.AddCommand(versionCommand)

	// Deprecated 'run' subcommand.
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Deprecated command. Use 'debug' instead.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("This command is deprecated, please use 'debug' instead.")
			os.Exit(0)
		},
	}
	RootCommand.AddCommand(runCommand)

	// 'debug' subcommand.
	debugCommand := &cobra.Command{
		Use:   "debug [package]",
		Short: "Compile and begin debugging program.",
		Long: `Compiles your program with optimizations disabled,
starts and attaches to it, and enables you to immediately begin debugging your program.`,
		Run: debugCmd,
	}
	RootCommand.AddCommand(debugCommand)

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
	RootCommand.AddCommand(execCommand)

	// 'trace' subcommand.
	traceCommand := &cobra.Command{
		Use:   "trace [package] regexp",
		Short: "Compile and begin tracing program.",
		Long:  "Trace program execution. Will set a tracepoint on every function matching the provided regular expression and output information when tracepoint is hit.",
		Run:   traceCmd,
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	traceCommand.Flags().IntVarP(&traceStackDepth, "stack", "s", 0, "Show stack trace with given depth.")
	RootCommand.AddCommand(traceCommand)

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test [package]",
		Short: "Compile test binary and begin debugging program.",
		Long:  `Compiles a test binary with optimizations disabled, starts and attaches to it, and enable you to immediately begin debugging your program.`,
		Run:   testCmd,
	}
	RootCommand.AddCommand(testCommand)

	// 'attach' subcommand.
	attachCommand := &cobra.Command{
		Use:   "attach pid",
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
	RootCommand.AddCommand(attachCommand)

	// 'connect' subcommand.
	connectCommand := &cobra.Command{
		Use:   "connect addr",
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
	RootCommand.AddCommand(connectCommand)

	return RootCommand
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		var pkg string
		dlvArgs, targetArgs := splitArgs(cmd, args)

		if len(dlvArgs) > 0 {
			pkg = args[0]
		}
		err := gobuild(debugname, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fp, err := filepath.Abs("./" + debugname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer os.Remove(fp)

		processArgs := append([]string{"./" + debugname}, targetArgs...)
		return execute(0, processArgs, conf)
	}()
	os.Exit(status)
}

func traceCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		var regexp string
		var processArgs []string

		dlvArgs, targetArgs := splitArgs(cmd, args)

		if traceAttachPid == 0 {
			var pkg string
			switch len(dlvArgs) {
			case 1:
				regexp = args[0]
			case 2:
				pkg = args[0]
				regexp = args[1]
			}
			if err := gobuild(debugname, pkg); err != nil {
				return 1
			}
			defer os.Remove("./" + debugname)

			processArgs = append([]string{"./" + debugname}, targetArgs...)
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
		funcs, err := client.ListFunctions(regexp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for i := range funcs {
			_, err = client.CreateBreakpoint(&api.Breakpoint{FunctionName: funcs[i], Tracepoint: true, Line: -1, Stacktrace: traceStackDepth})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		cmds := terminal.DebugCommands(client)
		t := terminal.New(client, nil)
		defer t.Close()
		err = cmds.Call("continue", "", t)
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
		var pkg string
		dlvArgs, targetArgs := splitArgs(cmd, args)

		if len(dlvArgs) > 0 {
			pkg = args[0]
		}
		err := gotestbuild(pkg)
		if err != nil {
			return 1
		}
		defer os.Remove("./" + testdebugname)
		processArgs := append([]string{"./" + testdebugname}, targetArgs...)

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

func splitArgs(cmd *cobra.Command, args []string) ([]string, []string) {
	if cmd.ArgsLenAtDash() >= 0 {
		return args[:cmd.ArgsLenAtDash()], args[cmd.ArgsLenAtDash():]
	}
	return args, []string{}
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
		AcceptMulti: AcceptMulti,
	}, Log)
	if err := server.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var status int
	if Headless {
		if !RootCommand.Flags().Changed("listen") {
			// Print listener address
			fmt.Printf("API server listening at: %s\n", listener.Addr())
		}
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

func gobuild(debugname, pkg string) error {
	args := []string{"-gcflags", "-N -l", "-o", debugname}
	if BuildFlags != "" {
		args = append(args, BuildFlags)
	}
	args = append(args, pkg)
	return gocommand("build", args...)
}

func gotestbuild(pkg string) error {
	args := []string{"-gcflags", "-N -l", "-c", "-o", testdebugname}
	if BuildFlags != "" {
		args = append(args, BuildFlags)
	}
	args = append(args, pkg)
	return gocommand("test", args...)
}

func gocommand(command string, args ...string) error {
	allargs := []string{command}
	allargs = append(allargs, args...)
	goBuild := exec.Command("go", allargs...)
	goBuild.Stderr = os.Stderr
	return goBuild.Run()
}
