package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/config"
	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
	"github.com/derekparker/delve/terminal"

	"github.com/spf13/cobra"
)

const version string = "0.10.0-alpha"

var Build string

var (
	Log        bool
	Headless   bool
	Addr       string
	InitFile   string
	BuildFlags string
)

func main() {
	// Config setup and load.
	conf := config.LoadConfig()

	// Main dlv root command.
	rootCommand := &cobra.Command{
		Use:   "dlv",
		Short: "Delve is a debugger for the Go programming language.",
		Long: `Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.
`,
	}
	rootCommand.PersistentFlags().StringVarP(&Addr, "listen", "l", "localhost:0", "Debugging server listen address.")
	rootCommand.PersistentFlags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	rootCommand.PersistentFlags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")
	rootCommand.PersistentFlags().StringVar(&InitFile, "init", "", "Init file, executed by the terminal client.")
	rootCommand.PersistentFlags().StringVar(&BuildFlags, "build-flags", "", "Build flags, to be passed to the compiler.")

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
		Run: func(cmd *cobra.Command, args []string) {
			status := func() int {
				const debugname = "debug"
				goBuild := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l", BuildFlags)
				goBuild.Stderr = os.Stderr
				err := goBuild.Run()
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
		},
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
	var traceAttachPid int
	traceCommand := &cobra.Command{
		Use:   "trace [regexp]",
		Short: "Compile and begin tracing program.",
		Long:  "Trace program execution. Will set a tracepoint on every function matching [regexp] and output information when tracepoint is hit.",
		Run: func(cmd *cobra.Command, args []string) {
			status := func() int {
				const debugname = "debug"
				var processArgs []string
				if traceAttachPid == 0 {
					goBuild := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l", BuildFlags)
					goBuild.Stderr = os.Stderr
					err := goBuild.Run()
					if err != nil {
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

				// Create and start a debugger server
				server := rpc.NewServer(&service.Config{
					Listener:    listener,
					ProcessArgs: processArgs,
					AttachPid:   traceAttachPid,
				}, Log)
				if err := server.Run(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				sigChan := make(chan os.Signal)
				signal.Notify(sigChan, sys.SIGINT)
				client := rpc.NewClient(listener.Addr().String())
				funcs, err := client.ListFunctions(args[0])
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				for i := range funcs {
					_, err := client.CreateBreakpoint(&api.Breakpoint{FunctionName: funcs[i], Tracepoint: true})
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						return 1
					}
				}
				stateChan := client.Continue()
				for {
					select {
					case state := <-stateChan:
						if state == nil {
							return 0
						}
						if state.Err != nil {
							fmt.Fprintln(os.Stderr, state.Err)
							return 0
						}
						for i := range state.Threads {
							th := state.Threads[i]
							if th.Breakpoint == nil {
								continue
							}
							var args []string
							var fname string
							if th.Function != nil {
								fname = th.Function.Name
							}
							if th.BreakpointInfo != nil {
								for _, arg := range th.BreakpointInfo.Arguments {
									args = append(args, arg.SinglelineString())
								}
							}
							fmt.Printf("%s(%s) %s:%d\n", fname, strings.Join(args, ", "), terminal.ShortenFilePath(th.File), th.Line)
						}
					case <-sigChan:
						server.Stop(traceAttachPid == 0)
						return 1
					}
				}
				return 0
			}()
			os.Exit(status)
		},
	}
	traceCommand.Flags().IntVarP(&traceAttachPid, "pid", "p", 0, "Pid to attach to.")
	rootCommand.AddCommand(traceCommand)

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test",
		Short: "Compile test binary and begin debugging program.",
		Long: `Compiles a test binary with optimizations disabled,
starts and attaches to it, and enable you to immediately begin debugging your program.`,
		Run: func(cmd *cobra.Command, args []string) {
			status := func() int {
				wd, err := os.Getwd()
				if err != nil {
					fmt.Fprintf(os.Stderr, err.Error())
					return 1
				}
				base := filepath.Base(wd)
				goTest := exec.Command("go", "test", "-c", "-gcflags", "-N -l", BuildFlags)
				goTest.Stderr = os.Stderr
				err = goTest.Run()
				if err != nil {
					return 1
				}
				debugname := "./" + base + ".test"
				defer os.Remove(debugname)
				processArgs := append([]string{debugname}, args...)

				return execute(0, processArgs, conf)
			}()
			os.Exit(status)
		},
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
		Run: func(cmd *cobra.Command, args []string) {
			pid, err := strconv.Atoi(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid pid: %s\n", args[0])
				os.Exit(1)
			}
			os.Exit(execute(pid, nil, conf))
		},
	}
	rootCommand.AddCommand(attachCommand)

	// 'connect' subcommand.
	connectCommand := &cobra.Command{
		Use:   "connect [addr]",
		Short: "Connect to a headless debug server.",
		Long:  "Connect to a headless debug server.",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				fmt.Fprintf(os.Stderr, "An address was not provided. You must provide an address as the first argument.\n")
				os.Exit(1)
			}
			addr := args[0]
			if addr == "" {
				fmt.Fprintf(os.Stderr, "An empty address was provided. You must provide an address as the first argument.\n")
				os.Exit(1)
			}
			os.Exit(connect(addr, conf))
		},
	}
	rootCommand.AddCommand(connectCommand)

	rootCommand.Execute()
}

func connect(addr string, conf *config.Config) int {
	// Create and start a terminal - attach to running instance
	var client service.Client
	client = rpc.NewClient(addr)
	term := terminal.New(client, conf)
	err, status := term.Run()
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
	if !Headless {
		// Create and start a terminal
		var client service.Client
		client = rpc.NewClient(listener.Addr().String())
		term := terminal.New(client, conf)
		term.InitFile = InitFile
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
