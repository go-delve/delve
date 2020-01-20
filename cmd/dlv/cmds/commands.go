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

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/pkg/version"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
	"github.com/spf13/cobra"
)

var (
	// Log is whether to log debug statements.
	Log bool
	// LogOutput is a comma separated list of components that should produce debug output.
	LogOutput string
	// LogDest is the file path or file descriptor where logs should go.
	LogDest string
	// Headless is whether to run without terminal.
	Headless bool
	// ContinueOnStart is whether to continue the process on startup
	ContinueOnStart bool
	// APIVersion is the requested API version while running headless
	APIVersion int
	// AcceptMulti allows multiple clients to connect to the same server
	AcceptMulti bool
	// Addr is the debugging server listen address.
	Addr string
	// InitFile is the path to initialization file.
	InitFile string
	// BuildFlags is the flags passed during compiler invocation.
	BuildFlags string
	// WorkingDir is the working directory for running the program.
	WorkingDir string
	// CheckLocalConnUser is true if the debugger should check that local
	// connections come from the same user that started the headless server
	CheckLocalConnUser bool

	// Backend selection
	Backend string

	// CheckGoVersion is true if the debugger should check the version of Go
	// used to compile the executable and refuse to work on incompatible
	// versions.
	CheckGoVersion bool

	// RootCommand is the root of the command tree.
	RootCommand *cobra.Command

	traceAttachPid  int
	traceExecFile   string
	traceTestBinary bool
	traceStackDepth int

	conf *config.Config
)

const dlvCommandLongDesc = `Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.

Pass flags to the program you are debugging using ` + "`--`" + `, for example:

` + "`dlv exec ./hello -- server --config conf/config.toml`"

// New returns an initialized command tree.
func New(docCall bool) *cobra.Command {
	// Config setup and load.
	conf = config.LoadConfig()
	buildFlagsDefault := ""
	if runtime.GOOS == "windows" {
		ver, _ := goversion.Installed()
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
			// Work-around for https://github.com/golang/go/issues/13154
			buildFlagsDefault = "-ldflags='-linkmode internal'"
		}
	}

	// Main dlv root command.
	RootCommand = &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long:  dlvCommandLongDesc,
	}

	RootCommand.PersistentFlags().StringVarP(&Addr, "listen", "l", "127.0.0.1:0", "Debugging server listen address.")

	RootCommand.PersistentFlags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	RootCommand.PersistentFlags().StringVarP(&LogOutput, "log-output", "", "", `Comma separated list of components that should produce debug output (see 'dlv help log')`)
	RootCommand.PersistentFlags().StringVarP(&LogDest, "log-dest", "", "", "Writes logs to the specified file or file descriptor (see 'dlv help log').")

	RootCommand.PersistentFlags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")
	RootCommand.PersistentFlags().BoolVarP(&AcceptMulti, "accept-multiclient", "", false, "Allows a headless server to accept multiple client connections.")
	RootCommand.PersistentFlags().IntVar(&APIVersion, "api-version", 1, "Selects API version when headless.")
	RootCommand.PersistentFlags().StringVar(&InitFile, "init", "", "Init file, executed by the terminal client.")
	RootCommand.PersistentFlags().StringVar(&BuildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler.")
	RootCommand.PersistentFlags().StringVar(&WorkingDir, "wd", ".", "Working directory for running the program.")
	RootCommand.PersistentFlags().BoolVarP(&CheckGoVersion, "check-go-version", "", true, "Checks that the version of Go in use is compatible with Delve.")
	RootCommand.PersistentFlags().BoolVarP(&CheckLocalConnUser, "only-same-user", "", true, "Only connections from the same user that started this instance of Delve are allowed to connect.")
	RootCommand.PersistentFlags().StringVar(&Backend, "backend", "default", `Backend selection (see 'dlv help backend').`)

	// 'attach' subcommand.
	attachCommand := &cobra.Command{
		Use:   "attach pid [executable]",
		Short: "Attach to running process and begin debugging.",
		Long: `Attach to an already running process and begin debugging it.

This command will cause Delve to take control of an already running process, and
begin a new debug session.  When exiting the debug session you will have the
option to let the process continue or kill it.
`,
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
		Long:  "Connect to a running headless debug server.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide an address as the first argument")
			}
			return nil
		},
		Run: connectCmd,
	}
	RootCommand.AddCommand(connectCommand)

	// 'debug' subcommand.
	debugCommand := &cobra.Command{
		Use:   "debug [package]",
		Short: "Compile and begin debugging main package in current directory, or the package specified.",
		Long: `Compiles your program with optimizations disabled, starts and attaches to it.

By default, with no arguments, Delve will compile the 'main' package in the
current directory, and begin to debug it. Alternatively you can specify a
package name and Delve will compile that package instead, and begin a new debug
session.`,
		Run: debugCmd,
	}
	debugCommand.Flags().String("output", "./__debug_bin", "Output path for the binary.")
	debugCommand.Flags().BoolVar(&ContinueOnStart, "continue", false, "Continue the debugged process on start.")
	RootCommand.AddCommand(debugCommand)

	// 'exec' subcommand.
	execCommand := &cobra.Command{
		Use:   "exec <path/to/binary>",
		Short: "Execute a precompiled binary, and begin a debug session.",
		Long: `Execute a precompiled binary and begin a debug session.

This command will cause Delve to exec the binary and immediately attach to it to
begin a new debug session. Please note that if the binary was not compiled with
optimizations disabled, it may be difficult to properly debug it. Please
consider compiling debugging binaries with -gcflags="all=-N -l" on Go 1.10
or later, -gcflags="-N -l" on earlier versions of Go.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("you must provide a path to a binary")
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(execute(0, args, conf, "", executingExistingFile))
		},
	}
	execCommand.Flags().BoolVar(&ContinueOnStart, "continue", false, "Continue the debugged process on start.")
	RootCommand.AddCommand(execCommand)

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

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test [package]",
		Short: "Compile test binary and begin debugging program.",
		Long: `Compiles a test binary with optimizations disabled and begins a new debug session.

The test command allows you to begin a new debug session in the context of your
unit tests. By default Delve will debug the tests in the current directory.
Alternatively you can specify a package name, and Delve will debug the tests in
that package instead.`,
		Run: testCmd,
	}
	testCommand.Flags().String("output", "debug.test", "Output path for the binary.")
	RootCommand.AddCommand(testCommand)

	// 'trace' subcommand.
	traceCommand := &cobra.Command{
		Use:   "trace [package] regexp",
		Short: "Compile and begin tracing program.",
		Long: `Trace program execution.

The trace sub command will set a tracepoint on every function matching the
provided regular expression and output information when tracepoint is hit.  This
is useful if you do not want to begin an entire debug session, but merely want
to know what functions your process is executing.`,
		Run: traceCmd,
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	traceCommand.Flags().StringVarP(&traceExecFile, "exec", "e", "", "Binary file to exec and trace.")
	traceCommand.Flags().BoolVarP(&traceTestBinary, "test", "t", false, "Trace a test binary.")
	traceCommand.Flags().IntVarP(&traceStackDepth, "stack", "s", 0, "Show stack trace with given depth.")
	traceCommand.Flags().String("output", "debug", "Output path for the binary.")
	RootCommand.AddCommand(traceCommand)

	coreCommand := &cobra.Command{
		Use:   "core <executable> <core>",
		Short: "Examine a core dump.",
		Long: `Examine a core dump.

The core command will open the specified core file and the associated
executable and let you examine the state of the process when the
core dump was taken.

Currently supports linux/amd64 core files and windows/amd64 minidumps.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("you must provide a core file and an executable")
			}
			return nil
		},
		Run: coreCmd,
	}
	RootCommand.AddCommand(coreCommand)

	// 'version' subcommand.
	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Delve Debugger\n%s\n", version.DelveVersion)
		},
	}
	RootCommand.AddCommand(versionCommand)

	if path, _ := exec.LookPath("rr"); path != "" || docCall {
		replayCommand := &cobra.Command{
			Use:   "replay [trace directory]",
			Short: "Replays a rr trace.",
			Long: `Replays a rr trace.

The replay command will open a trace generated by mozilla rr. Mozilla rr must be installed:
https://github.com/mozilla/rr
			`,
			PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return errors.New("you must provide a path to a binary")
				}
				return nil
			},
			Run: func(cmd *cobra.Command, args []string) {
				Backend = "rr"
				os.Exit(execute(0, []string{}, conf, args[0], executingOther))
			},
		}
		RootCommand.AddCommand(replayCommand)
	}

	RootCommand.AddCommand(&cobra.Command{
		Use:   "backend",
		Short: "Help about the --backend flag.",
		Long: `The --backend flag specifies which backend should be used, possible values
are:

	default		Uses lldb on macOS, native everywhere else.
	native		Native backend.
	lldb		Uses lldb-server or debugserver.
	rr		Uses mozilla rr (https://github.com/mozilla/rr).

`})

	RootCommand.AddCommand(&cobra.Command{
		Use:   "log",
		Short: "Help about logging flags.",
		Long: `Logging can be enabled by specifying the --log flag and using the
--log-output flag to select which components should produce logs.

The argument of --log-output must be a comma separated list of component
names selected from this list:


	debugger	Log debugger commands
	gdbwire		Log connection to gdbserial backend
	lldbout		Copy output from debugserver/lldb to standard output
	debuglineerr	Log recoverable errors reading .debug_line
	rpc		Log all RPC messages
	fncall		Log function call protocol
	minidump	Log minidump loading

Additionally --log-dest can be used to specify where the logs should be
written. 
If the argument is a number it will be interpreted as a file descriptor,
otherwise as a file path.
This option will also redirect the \"API listening\" message in headless
mode.

`,
	})

	RootCommand.DisableAutoGenTag = true

	return RootCommand
}

// Remove the file at path and issue a warning to stderr if this fails.
func remove(path string) {
	err := os.Remove(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not remove %v: %v\n", path, err)
	}
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		debugname, err := filepath.Abs(cmd.Flag("output").Value.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		dlvArgs, targetArgs := splitArgs(cmd, args)
		err = gobuild(debugname, dlvArgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)
		return execute(0, processArgs, conf, "", executingGeneratedFile)
	}()
	os.Exit(status)
}

func traceCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		err := logflags.Setup(Log, LogOutput, LogDest)
		defer logflags.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		if Headless {
			fmt.Fprintf(os.Stderr, "Warning: headless mode not supported with trace\n")
		}
		if AcceptMulti {
			fmt.Fprintf(os.Stderr, "Warning: accept multiclient mode not supported with trace")
		}

		var regexp string
		var processArgs []string

		dlvArgs, targetArgs := splitArgs(cmd, args)

		if traceAttachPid == 0 {
			var dlvArgsLen = len(dlvArgs)

			if dlvArgsLen == 1 {
				regexp = args[0]
				dlvArgs = dlvArgs[0:0]
			} else if dlvArgsLen >= 2 {
				if traceExecFile != "" {
					fmt.Fprintln(os.Stderr, "Cannot specify package when using exec.")
					return 1
				}
				regexp = dlvArgs[dlvArgsLen-1]
				dlvArgs = dlvArgs[:dlvArgsLen-1]
			}

			debugname := traceExecFile
			if traceExecFile == "" {
				debugname, err = filepath.Abs(cmd.Flag("output").Value.String())
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					return 1
				}
				if traceTestBinary {
					if err := gotestbuild(debugname, dlvArgs); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return 1
					}
				} else {
					if err := gobuild(debugname, dlvArgs); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return 1
					}
				}
				defer remove(debugname)
			}

			processArgs = append([]string{debugname}, targetArgs...)
		}

		// Make a local in-memory connection that client and server use to communicate
		listener, clientConn := service.ListenerPipe()
		defer listener.Close()

		// Create and start a debug server
		server := rpccommon.NewServer(&service.Config{
			Listener:       listener,
			ProcessArgs:    processArgs,
			AttachPid:      traceAttachPid,
			APIVersion:     2,
			WorkingDir:     WorkingDir,
			Backend:        Backend,
			CheckGoVersion: CheckGoVersion,
		})
		if err := server.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		client := rpc2.NewClientFromConn(clientConn)
		funcs, err := client.ListFunctions(regexp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for i := range funcs {
			_, err = client.CreateBreakpoint(&api.Breakpoint{
				FunctionName: funcs[i],
				Tracepoint:   true,
				Line:         -1,
				Stacktrace:   traceStackDepth,
				LoadArgs:     &terminal.ShortLoadConfig,
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			addrs, err := client.FunctionReturnLocations(funcs[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			for i := range addrs {
				_, err = client.CreateBreakpoint(&api.Breakpoint{
					Addr:        addrs[i],
					TraceReturn: true,
					Line:        -1,
					LoadArgs:    &terminal.ShortLoadConfig,
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			}
		}
		cmds := terminal.DebugCommands(client)
		t := terminal.New(client, nil)
		defer t.Close()
		err = cmds.Call("continue", t)
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
		debugname, err := filepath.Abs(cmd.Flag("output").Value.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		dlvArgs, targetArgs := splitArgs(cmd, args)
		err = gotestbuild(debugname, dlvArgs)
		if err != nil {
			return 1
		}
		defer remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)

		return execute(0, processArgs, conf, "", executingGeneratedTest)
	}()
	os.Exit(status)
}

func attachCmd(cmd *cobra.Command, args []string) {
	pid, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid pid: %s\n", args[0])
		os.Exit(1)
	}
	os.Exit(execute(pid, args[1:], conf, "", executingOther))
}

func coreCmd(cmd *cobra.Command, args []string) {
	os.Exit(execute(0, []string{args[0]}, conf, args[1], executingOther))
}

func connectCmd(cmd *cobra.Command, args []string) {
	addr := args[0]
	if addr == "" {
		fmt.Fprint(os.Stderr, "An empty address was provided. You must provide an address as the first argument.\n")
		os.Exit(1)
	}
	os.Exit(connect(addr, nil, conf, executingOther))
}

func splitArgs(cmd *cobra.Command, args []string) ([]string, []string) {
	if cmd.ArgsLenAtDash() >= 0 {
		return args[:cmd.ArgsLenAtDash()], args[cmd.ArgsLenAtDash():]
	}
	return args, []string{}
}

func connect(addr string, clientConn net.Conn, conf *config.Config, kind executeKind) int {
	// Create and start a terminal - attach to running instance
	var client *rpc2.RPCClient
	if clientConn != nil {
		client = rpc2.NewClientFromConn(clientConn)
	} else {
		client = rpc2.NewClient(addr)
	}
	if client.IsMulticlient() {
		state, _ := client.GetStateNonBlocking()
		// The error return of GetState will usually be the ErrProcessExited,
		// which we don't care about. If there are other errors they will show up
		// later, here we are only concerned about stopping a running target so
		// that we can initialize our connection.
		if state != nil && state.Running {
			_, err := client.Halt()
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not halt: %v", err)
				return 1
			}
		}
	}
	term := terminal.New(client, conf)
	term.InitFile = InitFile
	status, err := term.Run()
	if err != nil {
		fmt.Println(err)
	}
	return status
}

type executeKind int

const (
	executingExistingFile = executeKind(iota)
	executingGeneratedFile
	executingGeneratedTest
	executingOther
)

func execute(attachPid int, processArgs []string, conf *config.Config, coreFile string, kind executeKind) int {
	if err := logflags.Setup(Log, LogOutput, LogDest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer logflags.Close()

	if Headless && (InitFile != "") {
		fmt.Fprint(os.Stderr, "Warning: init file ignored with --headless\n")
	}
	if ContinueOnStart {
		if !Headless {
			fmt.Fprint(os.Stderr, "Error: --continue only works with --headless; use an init file\n")
			return 1
		}
		if !AcceptMulti {
			fmt.Fprint(os.Stderr, "Error: --continue requires --accept-multiclient\n")
			return 1
		}
	}

	if !Headless && AcceptMulti {
		fmt.Fprint(os.Stderr, "Warning accept-multi: ignored\n")
		// AcceptMulti won't work in normal (non-headless) mode because we always
		// call server.Stop after the terminal client exits.
		AcceptMulti = false
	}

	var listener net.Listener
	var clientConn net.Conn
	var err error

	// Make a TCP listener
	if Headless {
		listener, err = net.Listen("tcp", Addr)
	} else {
		listener, clientConn = service.ListenerPipe()
	}
	if err != nil {
		fmt.Printf("couldn't start listener: %s\n", err)
		return 1
	}
	defer listener.Close()

	var server service.Server

	disconnectChan := make(chan struct{})

	// Create and start a debugger server
	switch APIVersion {
	case 1, 2:
		server = rpccommon.NewServer(&service.Config{
			Listener:             listener,
			ProcessArgs:          processArgs,
			AttachPid:            attachPid,
			AcceptMulti:          AcceptMulti,
			APIVersion:           APIVersion,
			WorkingDir:           WorkingDir,
			Backend:              Backend,
			CoreFile:             coreFile,
			Foreground:           Headless,
			DebugInfoDirectories: conf.DebugInfoDirectories,
			CheckGoVersion:       CheckGoVersion,
			CheckLocalConnUser:   CheckLocalConnUser,

			DisconnectChan: disconnectChan,
		})
	default:
		fmt.Printf("Unknown API version: %d\n", APIVersion)
		return 1
	}

	if err := server.Run(); err != nil {
		if err == api.ErrNotExecutable {
			switch kind {
			case executingGeneratedFile:
				fmt.Fprintln(os.Stderr, "Can not debug non-main package")
				return 1
			case executingExistingFile:
				fmt.Fprintf(os.Stderr, "%s is not executable\n", processArgs[0])
				return 1
			default:
				// fallthrough
			}
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var status int
	if Headless {
		if ContinueOnStart {
			var client *rpc2.RPCClient
			client = rpc2.NewClient(listener.Addr().String())
			client.Disconnect(true) // true = continue after disconnect
		}
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT)
		if runtime.GOOS == "windows" {
			// On windows Ctrl-C sent to inferior process is delivered
			// as SIGINT to delve. Ignore it instead of stopping the server
			// in order to be able to debug signal handlers.
			go func() {
				for {
					select {
					case <-ch:
					}
				}
			}()
			select {
			case <-disconnectChan:
			}
		} else {
			select {
			case <-ch:
			case <-disconnectChan:
			}
		}
		err = server.Stop()
		if err != nil {
			fmt.Println(err)
		}

		return status
	}

	return connect(listener.Addr().String(), clientConn, conf, kind)
}

func optflags(args []string) []string {
	// after go1.9 building with -gcflags='-N -l' and -a simultaneously works.
	// after go1.10 specifying -a is unnecessary because of the new caching strategy, but we should pass -gcflags=all=-N -l to have it applied to all packages
	// see https://github.com/golang/go/commit/5993251c015dfa1e905bdf44bdb41572387edf90

	ver, _ := goversion.Installed()
	switch {
	case ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}):
		args = append(args, "-gcflags", "all=-N -l")
	case ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}):
		args = append(args, "-gcflags", "-N -l", "-a")
	default:
		args = append(args, "-gcflags", "-N -l")
	}
	return args
}

func gobuild(debugname string, pkgs []string) error {
	args := []string{"-o", debugname}
	args = optflags(args)
	if BuildFlags != "" {
		args = append(args, config.SplitQuotedFields(BuildFlags, '\'')...)
	}
	args = append(args, pkgs...)
	return gocommand("build", args...)
}

func gotestbuild(debugname string, pkgs []string) error {
	args := []string{"-c", "-o", debugname}
	args = optflags(args)
	if BuildFlags != "" {
		args = append(args, config.SplitQuotedFields(BuildFlags, '\'')...)
	}
	args = append(args, pkgs...)
	return gocommand("test", args...)
}

func gocommand(command string, args ...string) error {
	allargs := []string{command}
	allargs = append(allargs, args...)
	goBuild := exec.Command("go", allargs...)
	goBuild.Stderr = os.Stderr
	return goBuild.Run()
}
