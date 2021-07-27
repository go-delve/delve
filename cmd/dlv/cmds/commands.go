package cmds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/pkg/version"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/dap"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	// log is whether to log debug statements.
	log bool
	// logOutput is a comma separated list of components that should produce debug output.
	logOutput string
	// logDest is the file path or file descriptor where logs should go.
	logDest string
	// headless is whether to run without terminal.
	headless bool
	// continueOnStart is whether to continue the process on startup
	continueOnStart bool
	// apiVersion is the requested API version while running headless
	apiVersion int
	// acceptMulti allows multiple clients to connect to the same server
	acceptMulti bool
	// addr is the debugging server listen address.
	addr string
	// initFile is the path to initialization file.
	initFile string
	// buildFlags is the flags passed during compiler invocation.
	buildFlags string
	// workingDir is the working directory for running the program.
	workingDir string
	// checkLocalConnUser is true if the debugger should check that local
	// connections come from the same user that started the headless server
	checkLocalConnUser bool
	// tty is used to provide an alternate TTY for the program you wish to debug.
	tty string
	// disableASLR is used to disable ASLR
	disableASLR bool

	// backend selection
	backend string

	// checkGoVersion is true if the debugger should check the version of Go
	// used to compile the executable and refuse to work on incompatible
	// versions.
	checkGoVersion bool

	// rootCommand is the root of the command tree.
	rootCommand *cobra.Command

	traceAttachPid  int
	traceExecFile   string
	traceTestBinary bool
	traceStackDepth int
	traceUseEBPF    bool

	// redirect specifications for target process
	redirects []string

	allowNonTerminalInteractive bool

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
		if ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
			// Work-around for https://github.com/golang/go/issues/13154
			buildFlagsDefault = "-ldflags='-linkmode internal'"
		}
	}

	// Main dlv root command.
	rootCommand = &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long:  dlvCommandLongDesc,
	}

	rootCommand.PersistentFlags().StringVarP(&addr, "listen", "l", "127.0.0.1:0", "Debugging server listen address.")

	rootCommand.PersistentFlags().BoolVarP(&log, "log", "", false, "Enable debugging server logging.")
	rootCommand.PersistentFlags().StringVarP(&logOutput, "log-output", "", "", `Comma separated list of components that should produce debug output (see 'dlv help log')`)
	rootCommand.PersistentFlags().StringVarP(&logDest, "log-dest", "", "", "Writes logs to the specified file or file descriptor (see 'dlv help log').")

	rootCommand.PersistentFlags().BoolVarP(&headless, "headless", "", false, "Run debug server only, in headless mode.")
	rootCommand.PersistentFlags().BoolVarP(&acceptMulti, "accept-multiclient", "", false, "Allows a headless server to accept multiple client connections.")
	rootCommand.PersistentFlags().IntVar(&apiVersion, "api-version", 1, "Selects API version when headless. New clients should use v2. Can be reset via RPCServer.SetApiVersion. See Documentation/api/json-rpc/README.md.")
	rootCommand.PersistentFlags().StringVar(&initFile, "init", "", "Init file, executed by the terminal client.")
	rootCommand.PersistentFlags().StringVar(&buildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler. For example: --build-flags=\"-tags=integration -mod=vendor -cover -v\"")
	rootCommand.PersistentFlags().StringVar(&workingDir, "wd", "", "Working directory for running the program.")
	rootCommand.PersistentFlags().BoolVarP(&checkGoVersion, "check-go-version", "", true, "Checks that the version of Go in use is compatible with Delve.")
	rootCommand.PersistentFlags().BoolVarP(&checkLocalConnUser, "only-same-user", "", true, "Only connections from the same user that started this instance of Delve are allowed to connect.")
	rootCommand.PersistentFlags().StringVar(&backend, "backend", "default", `Backend selection (see 'dlv help backend').`)
	rootCommand.PersistentFlags().StringArrayVarP(&redirects, "redirect", "r", []string{}, "Specifies redirect rules for target process (see 'dlv help redirect')")
	rootCommand.PersistentFlags().BoolVar(&allowNonTerminalInteractive, "allow-non-terminal-interactive", false, "Allows interactive sessions of Delve that don't have a terminal as stdin, stdout and stderr")
	rootCommand.PersistentFlags().BoolVar(&disableASLR, "disable-aslr", false, "Disables address space randomization")

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
	attachCommand.Flags().BoolVar(&continueOnStart, "continue", false, "Continue the debugged process on start.")
	rootCommand.AddCommand(attachCommand)

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
	rootCommand.AddCommand(connectCommand)

	// 'dap' subcommand.
	dapCommand := &cobra.Command{
		Use:   "dap",
		Short: "[EXPERIMENTAL] Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).",
		Long: `[EXPERIMENTAL] Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).

The server is always headless and requires a DAP client like vscode to connect and request a binary
to be launched or process to be attached to. The following modes are supported:
- launch + exec (executes precompiled binary, like 'dlv exec')
- launch + debug (builds and launches, like 'dlv debug')
- launch + test (builds and tests, like 'dlv test')
- launch + replay (replays an rr trace, like 'dlv replay')
- launch + core (replays a core dump file, like 'dlv core')
- attach + local (attaches to a running process, like 'dlv attach')
The server does not yet accept multiple client connections (--accept-multiclient).
While --continue is not supported, stopOnEntry launch/attach attribute can be used to control if
execution is resumed at the start of the debug session.`,
		Run: dapCmd,
	}
	// TODO(polina): support --tty when dlv dap allows to launch a program from command-line
	rootCommand.AddCommand(dapCommand)

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
	debugCommand.Flags().BoolVar(&continueOnStart, "continue", false, "Continue the debugged process on start.")
	debugCommand.Flags().StringVar(&tty, "tty", "", "TTY to use for the target program")
	rootCommand.AddCommand(debugCommand)

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
			os.Exit(execute(0, args, conf, "", debugger.ExecutingExistingFile, args, buildFlags))
		},
	}
	execCommand.Flags().StringVar(&tty, "tty", "", "TTY to use for the target program")
	execCommand.Flags().BoolVar(&continueOnStart, "continue", false, "Continue the debugged process on start.")
	rootCommand.AddCommand(execCommand)

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

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test [package]",
		Short: "Compile test binary and begin debugging program.",
		Long: `Compiles a test binary with optimizations disabled and begins a new debug session.

The test command allows you to begin a new debug session in the context of your
unit tests. By default Delve will debug the tests in the current directory.
Alternatively you can specify a package name, and Delve will debug the tests in
that package instead. Double-dashes ` + "`--`" + ` can be used to pass arguments to the test program:

dlv test [package] -- -test.v -other-argument

See also: 'go help testflag'.`,
		Run: testCmd,
	}
	testCommand.Flags().String("output", "debug.test", "Output path for the binary.")
	rootCommand.AddCommand(testCommand)

	// 'trace' subcommand.
	traceCommand := &cobra.Command{
		Use:   "trace [package] regexp",
		Short: "Compile and begin tracing program.",
		Long: `Trace program execution.

The trace sub command will set a tracepoint on every function matching the
provided regular expression and output information when tracepoint is hit.  This
is useful if you do not want to begin an entire debug session, but merely want
to know what functions your process is executing.

The output of the trace sub command is printed to stderr, so if you would like to
only see the output of the trace operations you can redirect stdout.`,
		Run: traceCmd,
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	traceCommand.Flags().StringVarP(&traceExecFile, "exec", "e", "", "Binary file to exec and trace.")
	traceCommand.Flags().BoolVarP(&traceTestBinary, "test", "t", false, "Trace a test binary.")
	traceCommand.Flags().BoolVarP(&traceUseEBPF, "ebpf", "", false, "Trace using eBPF (experimental).")
	traceCommand.Flags().IntVarP(&traceStackDepth, "stack", "s", 0, "Show stack trace with given depth. (Ignored with -ebpf)")
	traceCommand.Flags().String("output", "debug", "Output path for the binary.")
	rootCommand.AddCommand(traceCommand)

	coreCommand := &cobra.Command{
		Use:   "core <executable> <core>",
		Short: "Examine a core dump.",
		Long: `Examine a core dump (only supports linux and windows core dumps).

The core command will open the specified core file and the associated
executable and let you examine the state of the process when the
core dump was taken.

Currently supports linux/amd64 and linux/arm64 core files, windows/amd64 minidumps and core files generated by Delve's 'dump' command.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("you must provide a core file and an executable")
			}
			return nil
		},
		Run: coreCmd,
	}
	rootCommand.AddCommand(coreCommand)

	// 'version' subcommand.
	var versionVerbose = false
	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Delve Debugger\n%s\n", version.DelveVersion)
			if versionVerbose {
				fmt.Printf("Build Details: %s\n", version.BuildInfo())
			}
		},
	}
	versionCommand.Flags().BoolVarP(&versionVerbose, "verbose", "v", false, "print verbose version info")
	rootCommand.AddCommand(versionCommand)

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
				backend = "rr"
				os.Exit(execute(0, []string{}, conf, args[0], debugger.ExecutingOther, args, buildFlags))
			},
		}
		rootCommand.AddCommand(replayCommand)
	}

	rootCommand.AddCommand(&cobra.Command{
		Use:   "backend",
		Short: "Help about the --backend flag.",
		Long: `The --backend flag specifies which backend should be used, possible values
are:

	default		Uses lldb on macOS, native everywhere else.
	native		Native backend.
	lldb		Uses lldb-server or debugserver.
	rr		Uses mozilla rr (https://github.com/mozilla/rr).

`})

	rootCommand.AddCommand(&cobra.Command{
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
	dap		Log all DAP messages
	fncall		Log function call protocol
	minidump	Log minidump loading

Additionally --log-dest can be used to specify where the logs should be
written. 
If the argument is a number it will be interpreted as a file descriptor,
otherwise as a file path.
This option will also redirect the "server listening at" message in headless
and dap modes.

`,
	})

	rootCommand.AddCommand(&cobra.Command{
		Use:   "redirect",
		Short: "Help about file redirection.",
		Long: `The standard file descriptors of the target process can be controlled using the '-r' and '--tty' arguments. 

The --tty argument allows redirecting all standard descriptors to a terminal, specified as an argument to --tty.

The syntax for '-r' argument is:

		-r [source:]destination

Where source is one of 'stdin', 'stdout' or 'stderr' and destination is the path to a file. If the source is omitted stdin is used implicitly.

File redirects can also be changed using the 'restart' command.
`,
	})

	rootCommand.DisableAutoGenTag = true

	return rootCommand
}

func dapCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		if err := logflags.Setup(log, logOutput, logDest); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer logflags.Close()

		if cmd.Flag("headless").Changed {
			fmt.Fprintf(os.Stderr, "Warning: dap mode is always headless\n")
		}
		if acceptMulti {
			fmt.Fprintf(os.Stderr, "Warning: accept-multiclient mode not supported with dap\n")
		}
		if initFile != "" {
			fmt.Fprint(os.Stderr, "Warning: init file ignored with dap\n")
		}
		if continueOnStart {
			fmt.Fprintf(os.Stderr, "Warning: continue ignored with dap; specify via launch/attach request instead\n")
		}
		if backend != "default" {
			fmt.Fprintf(os.Stderr, "Warning: backend ignored with dap; specify via launch/attach request instead\n")
		}
		if buildFlags != "" {
			fmt.Fprintf(os.Stderr, "Warning: build flags ignored with dap; specify via launch/attach request instead\n")
		}
		if workingDir != "" {
			fmt.Fprintf(os.Stderr, "Warning: working directory ignored with dap: specify via launch request instead\n")
		}
		dlvArgs, targetArgs := splitArgs(cmd, args)
		if len(dlvArgs) > 0 {
			fmt.Fprintf(os.Stderr, "Warning: debug arguments ignored with dap; specify via launch/attach request instead\n")
		}
		if len(targetArgs) > 0 {
			fmt.Fprintf(os.Stderr, "Warning: program flags ignored with dap; specify via launch/attach request instead\n")
		}

		listener, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Printf("couldn't start listener: %s\n", err)
			return 1
		}
		disconnectChan := make(chan struct{})
		server := dap.NewServer(&service.Config{
			Listener:       listener,
			DisconnectChan: disconnectChan,
			Debugger: debugger.Config{
				Backend:              backend,
				Foreground:           true, // server always runs without terminal client
				DebugInfoDirectories: conf.DebugInfoDirectories,
				CheckGoVersion:       checkGoVersion,
			},
			CheckLocalConnUser: checkLocalConnUser,
		})
		defer server.Stop()

		server.Run()
		waitForDisconnectSignal(disconnectChan)
		return 0
	}()
	os.Exit(status)
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		debugname, err := filepath.Abs(cmd.Flag("output").Value.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		dlvArgs, targetArgs := splitArgs(cmd, args)
		err = gobuild.GoBuild(debugname, dlvArgs, buildFlags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer gobuild.Remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)
		return execute(0, processArgs, conf, "", debugger.ExecutingGeneratedFile, dlvArgs, buildFlags)
	}()
	os.Exit(status)
}

func traceCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		err := logflags.Setup(log, logOutput, logDest)
		defer logflags.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		if headless {
			fmt.Fprintf(os.Stderr, "Warning: headless mode not supported with trace\n")
		}
		if acceptMulti {
			fmt.Fprintf(os.Stderr, "Warning: accept multiclient mode not supported with trace")
		}

		var regexp string
		var processArgs []string

		dlvArgs, targetArgs := splitArgs(cmd, args)
		var dlvArgsLen = len(dlvArgs)
		if dlvArgsLen == 1 {
			regexp = args[0]
			dlvArgs = dlvArgs[0:0]
		} else if dlvArgsLen >= 2 {
			regexp = dlvArgs[dlvArgsLen-1]
			dlvArgs = dlvArgs[:dlvArgsLen-1]
		}

		var debugname string
		if traceAttachPid == 0 {
			if dlvArgsLen >= 2 && traceExecFile != "" {
				fmt.Fprintln(os.Stderr, "Cannot specify package when using exec.")
				return 1
			}

			debugname = traceExecFile
			if traceExecFile == "" {
				debugname, err = filepath.Abs(cmd.Flag("output").Value.String())
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					return 1
				}
				if traceTestBinary {
					if err := gobuild.GoTestBuild(debugname, dlvArgs, buildFlags); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return 1
					}
				} else {
					if err := gobuild.GoBuild(debugname, dlvArgs, buildFlags); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return 1
					}
				}
				defer gobuild.Remove(debugname)
			}

			processArgs = append([]string{debugname}, targetArgs...)
		}

		// Make a local in-memory connection that client and server use to communicate
		listener, clientConn := service.ListenerPipe()
		defer listener.Close()

		if workingDir == "" {
			workingDir = "."
		}

		// Create and start a debug server
		server := rpccommon.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: processArgs,
			APIVersion:  2,
			Debugger: debugger.Config{
				AttachPid:      traceAttachPid,
				WorkingDir:     workingDir,
				Backend:        backend,
				CheckGoVersion: checkGoVersion,
			},
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
			if traceUseEBPF {
				err := client.CreateTracepoint(funcs[i])
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			} else {
				// Fall back to breakpoint based tracing if we get an error.
				_, err = client.CreateBreakpoint(&api.Breakpoint{
					FunctionName: funcs[i],
					Tracepoint:   true,
					Line:         -1,
					Stacktrace:   traceStackDepth,
					LoadArgs:     &terminal.ShortLoadConfig,
				})
				if err != nil && !isBreakpointExistsErr(err) {
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
						Stacktrace:  traceStackDepth,
						Line:        -1,
						LoadArgs:    &terminal.ShortLoadConfig,
					})
					if err != nil && !isBreakpointExistsErr(err) {
						fmt.Fprintln(os.Stderr, err)
						return 1
					}
				}
			}
		}
		cmds := terminal.DebugCommands(client)
		t := terminal.New(client, nil)
		defer t.Close()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					tracepoints, err := client.GetBufferedTracepoints()
					if err != nil {
						panic(err)
					}
					for _, t := range tracepoints {
						var params strings.Builder
						for _, p := range t.InputParams {
							if params.Len() > 0 {
								params.WriteString(", ")
							}
							if p.Kind == reflect.String {
								params.WriteString(fmt.Sprintf("%q", p.Value))
							} else {
								params.WriteString(p.Value)
							}
						}
						fmt.Printf("%s:%d %s(%s)\n", t.File, t.Line, t.FunctionName, params.String())
					}
				}
			}
		}()
		cmds.Call("continue", t)
		close(done)
		return 0
	}()
	os.Exit(status)
}

func isBreakpointExistsErr(err error) bool {
	return strings.Contains(err.Error(), "Breakpoint exists")
}

func testCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		debugname, err := filepath.Abs(cmd.Flag("output").Value.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		dlvArgs, targetArgs := splitArgs(cmd, args)
		err = gobuild.GoTestBuild(debugname, dlvArgs, buildFlags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer gobuild.Remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)

		if workingDir == "" {
			if len(dlvArgs) == 1 {
				workingDir = getPackageDir(dlvArgs[0])
			} else {
				workingDir = "."
			}
		}

		return execute(0, processArgs, conf, "", debugger.ExecutingGeneratedTest, dlvArgs, buildFlags)
	}()
	os.Exit(status)
}

func getPackageDir(pkg string) string {
	out, err := exec.Command("go", "list", "--json", pkg).CombinedOutput()
	if err != nil {
		return "."
	}
	type listOut struct {
		Dir string `json:"Dir"`
	}
	var listout listOut
	err = json.Unmarshal(out, &listout)
	if err != nil {
		return "."
	}
	return listout.Dir
}

func attachCmd(cmd *cobra.Command, args []string) {
	pid, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid pid: %s\n", args[0])
		os.Exit(1)
	}
	os.Exit(execute(pid, args[1:], conf, "", debugger.ExecutingOther, args, buildFlags))
}

func coreCmd(cmd *cobra.Command, args []string) {
	os.Exit(execute(0, []string{args[0]}, conf, args[1], debugger.ExecutingOther, args, buildFlags))
}

func connectCmd(cmd *cobra.Command, args []string) {
	addr := args[0]
	if addr == "" {
		fmt.Fprint(os.Stderr, "An empty address was provided. You must provide an address as the first argument.\n")
		os.Exit(1)
	}
	os.Exit(connect(addr, nil, conf, debugger.ExecutingOther))
}

// waitForDisconnectSignal is a blocking function that waits for either
// a SIGINT (Ctrl-C) or SIGTERM (kill -15) OS signal or for disconnectChan
// to be closed by the server when the client disconnects.
// Note that in headless mode, the debugged process is foregrounded
// (to have control of the tty for debugging interactive programs),
// so SIGINT gets sent to the debuggee and not to delve.
func waitForDisconnectSignal(disconnectChan chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
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
}

func splitArgs(cmd *cobra.Command, args []string) ([]string, []string) {
	if cmd.ArgsLenAtDash() >= 0 {
		return args[:cmd.ArgsLenAtDash()], args[cmd.ArgsLenAtDash():]
	}
	return args, []string{}
}

func connect(addr string, clientConn net.Conn, conf *config.Config, kind debugger.ExecuteKind) int {
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
	term.InitFile = initFile
	status, err := term.Run()
	if err != nil {
		fmt.Println(err)
	}
	return status
}

func execute(attachPid int, processArgs []string, conf *config.Config, coreFile string, kind debugger.ExecuteKind, dlvArgs []string, buildFlags string) int {
	if err := logflags.Setup(log, logOutput, logDest); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer logflags.Close()

	if headless && (initFile != "") {
		fmt.Fprint(os.Stderr, "Warning: init file ignored with --headless\n")
	}
	if continueOnStart {
		if !headless {
			fmt.Fprint(os.Stderr, "Error: --continue only works with --headless; use an init file\n")
			return 1
		}
		if !acceptMulti {
			fmt.Fprint(os.Stderr, "Error: --continue requires --accept-multiclient\n")
			return 1
		}
	}

	if !headless && acceptMulti {
		fmt.Fprint(os.Stderr, "Warning accept-multi: ignored\n")
		// acceptMulti won't work in normal (non-headless) mode because we always
		// call server.Stop after the terminal client exits.
		acceptMulti = false
	}

	if !headless && !allowNonTerminalInteractive {
		for _, f := range []struct {
			name string
			file *os.File
		}{{"Stdin", os.Stdin}, {"Stdout", os.Stdout}, {"Stderr", os.Stderr}} {
			if f.file == nil {
				continue
			}
			if !isatty.IsTerminal(f.file.Fd()) {
				fmt.Fprintf(os.Stderr, "%s is not a terminal, use '-r' to specify redirects for the target process or --allow-non-terminal-interactive=true if you really want to specify a redirect for Delve\n", f.name)
				return 1
			}
		}
	}

	if len(redirects) > 0 && tty != "" {
		fmt.Fprintf(os.Stderr, "Can not use -r and --tty together\n")
		return 1
	}

	redirects, err := parseRedirects(redirects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	var listener net.Listener
	var clientConn net.Conn

	// Make a TCP listener
	if headless {
		listener, err = net.Listen("tcp", addr)
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

	if workingDir == "" {
		workingDir = "."
	}

	// Create and start a debugger server
	switch apiVersion {
	case 1, 2:
		server = rpccommon.NewServer(&service.Config{
			Listener:           listener,
			ProcessArgs:        processArgs,
			AcceptMulti:        acceptMulti,
			APIVersion:         apiVersion,
			CheckLocalConnUser: checkLocalConnUser,
			DisconnectChan:     disconnectChan,
			Debugger: debugger.Config{
				AttachPid:            attachPid,
				WorkingDir:           workingDir,
				Backend:              backend,
				CoreFile:             coreFile,
				Foreground:           headless && tty == "",
				Packages:             dlvArgs,
				BuildFlags:           buildFlags,
				ExecuteKind:          kind,
				DebugInfoDirectories: conf.DebugInfoDirectories,
				CheckGoVersion:       checkGoVersion,
				TTY:                  tty,
				Redirects:            redirects,
				DisableASLR:          disableASLR,
			},
		})
	default:
		fmt.Printf("Unknown API version: %d\n", apiVersion)
		return 1
	}

	if err := server.Run(); err != nil {
		if err == api.ErrNotExecutable {
			switch kind {
			case debugger.ExecutingGeneratedFile:
				fmt.Fprintln(os.Stderr, "Can not debug non-main package")
				return 1
			case debugger.ExecutingExistingFile:
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
	if headless {
		if continueOnStart {
			client := rpc2.NewClient(listener.Addr().String())
			client.Disconnect(true) // true = continue after disconnect
		}
		waitForDisconnectSignal(disconnectChan)
		err = server.Stop()
		if err != nil {
			fmt.Println(err)
		}

		return status
	}

	return connect(listener.Addr().String(), clientConn, conf, kind)
}

func parseRedirects(redirects []string) ([3]string, error) {
	r := [3]string{}
	names := [3]string{"stdin", "stdout", "stderr"}
	for _, redirect := range redirects {
		idx := 0
		for i, name := range names {
			pfx := name + ":"
			if strings.HasPrefix(redirect, pfx) {
				idx = i
				redirect = redirect[len(pfx):]
				break
			}
		}
		if r[idx] != "" {
			return r, fmt.Errorf("redirect error: %s redirected twice", names[idx])
		}
		r[idx] = redirect
	}
	return r, nil
}
