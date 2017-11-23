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
	"strings"
	"syscall"

	"github.com/derekparker/delve/pkg/config"
	"github.com/derekparker/delve/pkg/goversion"
	"github.com/derekparker/delve/pkg/terminal"
	"github.com/derekparker/delve/pkg/version"
	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc2"
	"github.com/derekparker/delve/service/rpccommon"
	"github.com/spf13/cobra"
)

var (
	// Log is whether to log debug statements.
	Log bool
	// Headless is whether to run without terminal.
	Headless bool
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

	// Backend selection
	Backend string

	// RootCommand is the root of the command tree.
	RootCommand *cobra.Command

	traceAttachPid  int
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

	RootCommand.PersistentFlags().StringVarP(&Addr, "listen", "l", "localhost:0", "Debugging server listen address.")
	RootCommand.PersistentFlags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	RootCommand.PersistentFlags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")
	RootCommand.PersistentFlags().BoolVarP(&AcceptMulti, "accept-multiclient", "", false, "Allows a headless server to accept multiple client connections. Note that the server API is not reentrant and clients will have to coordinate.")
	RootCommand.PersistentFlags().IntVar(&APIVersion, "api-version", 1, "Selects API version when headless.")
	RootCommand.PersistentFlags().StringVar(&InitFile, "init", "", "Init file, executed by the terminal client.")
	RootCommand.PersistentFlags().StringVar(&BuildFlags, "build-flags", buildFlagsDefault, "Build flags, to be passed to the compiler.")
	RootCommand.PersistentFlags().StringVar(&WorkingDir, "wd", ".", "Working directory for running the program.")
	RootCommand.PersistentFlags().StringVar(&Backend, "backend", "default", `Backend selection:
	default		Uses lldb on macOS, native everywhere else.
	native		Native backend.
	lldb		Uses lldb-server or debugserver.
	rr		Uses mozilla rr (https://github.com/mozilla/rr).
`)

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
	debugCommand.Flags().String("output", "debug", "Output path for the binary.")
	RootCommand.AddCommand(debugCommand)

	// 'exec' subcommand.
	execCommand := &cobra.Command{
		Use:   "exec <path/to/binary>",
		Short: "Execute a precompiled binary, and begin a debug session.",
		Long: `Execute a precompiled binary and begin a debug session.

This command will cause Delve to exec the binary and immediately attach to it to
begin a new debug session. Please note that if the binary was not compiled with
optimizations disabled, it may be difficult to properly debug it. Please
consider compiling debugging binaries with -gcflags="-N -l".`,
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
	traceCommand.Flags().IntVarP(&traceStackDepth, "stack", "s", 0, "Show stack trace with given depth.")
	traceCommand.Flags().String("output", "debug", "Output path for the binary.")
	RootCommand.AddCommand(traceCommand)

	coreCommand := &cobra.Command{
		Use:   "core <executable> <core>",
		Short: "Examine a core dump.",
		Long: `Examine a core dump.

The core command will open the specified core file and the associated
executable and let you examine the state of the process when the
core dump was taken.`,
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

	return RootCommand
}

func debugCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		debugname := cmd.Flag("output").Value.String()
		if !strings.HasPrefix(debugname, "/") {
			debugname = "./" + debugname
		}
		debugname, err := filepath.Abs(debugname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		var pkg string
		dlvArgs, targetArgs := splitArgs(cmd, args)

		if len(dlvArgs) > 0 {
			pkg = args[0]
		}
		err = gobuild(debugname, pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		defer os.Remove(debugname)
		processArgs := append([]string{debugname}, targetArgs...)
		return execute(0, processArgs, conf, "", executingGeneratedFile)
	}()
	os.Exit(status)
}

func traceCmd(cmd *cobra.Command, args []string) {
	status := func() int {
		debugname := cmd.Flag("output").Value.String()
		if !strings.HasPrefix(debugname, "/") {
			debugname = "./" + debugname
		}
		debugname, err := filepath.Abs(debugname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

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
			defer os.Remove(debugname)

			processArgs = append([]string{debugname}, targetArgs...)
		}
		// Make a TCP listener
		listener, err := net.Listen("tcp", Addr)
		if err != nil {
			fmt.Printf("couldn't start listener: %s\n", err)
			return 1
		}
		defer listener.Close()

		// Create and start a debug server
		server := rpccommon.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: processArgs,
			AttachPid:   traceAttachPid,
			APIVersion:  2,
			WorkingDir:  WorkingDir,
			Backend:     Backend,
		}, Log)
		if err := server.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		client := rpc2.NewClient(listener.Addr().String())
		funcs, err := client.ListFunctions(regexp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for i := range funcs {
			_, err = client.CreateBreakpoint(&api.Breakpoint{FunctionName: funcs[i], Tracepoint: true, Line: -1, Stacktrace: traceStackDepth, LoadArgs: &terminal.ShortLoadConfig})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
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
		debugname := cmd.Flag("output").Value.String()
		if !strings.HasPrefix(debugname, "/") {
			debugname = "./" + debugname
		}
		debugname, err := filepath.Abs(debugname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}

		var pkg string
		dlvArgs, targetArgs := splitArgs(cmd, args)

		if len(dlvArgs) > 0 {
			pkg = args[0]
		}
		err = gotestbuild(debugname, pkg)
		if err != nil {
			return 1
		}
		defer os.Remove(debugname)
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
	client := rpc2.NewClient(addr)
	term := terminal.New(client, conf)
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
	// Make a TCP listener
	listener, err := net.Listen("tcp", Addr)
	if err != nil {
		fmt.Printf("couldn't start listener: %s\n", err)
		return 1
	}
	defer listener.Close()

	if Headless && (InitFile != "") {
		fmt.Fprint(os.Stderr, "Warning: init file ignored\n")
	}

	var server interface {
		Run() error
		Stop(bool) error
	}

	disconnectChan := make(chan struct{})

	// Create and start a debugger server
	switch APIVersion {
	case 1, 2:
		server = rpccommon.NewServer(&service.Config{
			Listener:    listener,
			ProcessArgs: processArgs,
			AttachPid:   attachPid,
			AcceptMulti: AcceptMulti,
			APIVersion:  APIVersion,
			WorkingDir:  WorkingDir,
			Backend:     Backend,
			CoreFile:    coreFile,

			DisconnectChan: disconnectChan,
		}, Log)
	default:
		fmt.Printf("Unknown API version: %d\n", APIVersion)
		return 1
	}

	if err := server.Run(); err != nil {
		if err == api.NotExecutableErr {
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
		// Print listener address
		fmt.Printf("API server listening at: %s\n", listener.Addr())
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT)
		select {
		case <-ch:
		case <-disconnectChan:
		}
		err = server.Stop(true)
	} else {
		// Create and start a terminal
		client := rpc2.NewClient(listener.Addr().String())
		if client.Recorded() && (kind == executingGeneratedFile || kind == executingGeneratedTest) {
			// When using the rr backend remove the trace directory if we built the
			// executable
			if tracedir, err := client.TraceDirectory(); err == nil {
				defer SafeRemoveAll(tracedir)
			}
		}
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
		args = append(args, config.SplitQuotedFields(BuildFlags, '\'')...)
	}
	if ver, _ := goversion.Installed(); ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
		// after go1.9 building with -gcflags='-N -l' and -a simultaneously works
		args = append(args, "-a")
	}
	args = append(args, pkg)
	return gocommand("build", args...)
}

func gotestbuild(debugname, pkg string) error {
	args := []string{"-gcflags", "-N -l", "-c", "-o", debugname}
	if BuildFlags != "" {
		args = append(args, config.SplitQuotedFields(BuildFlags, '\'')...)
	}
	if ver, _ := goversion.Installed(); ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}) {
		// after go1.9 building with -gcflags='-N -l' and -a simultaneously works
		args = append(args, "-a")
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

// SafeRemoveAll removes dir and its contents but only as long as dir does
// not contain directories.
func SafeRemoveAll(dir string) {
	dh, err := os.Open(dir)
	if err != nil {
		return
	}
	defer dh.Close()
	fis, err := dh.Readdir(-1)
	if err != nil {
		return
	}
	for _, fi := range fis {
		if fi.IsDir() {
			return
		}
	}
	for _, fi := range fis {
		if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil {
			return
		}
	}
	os.Remove(dir)
}
