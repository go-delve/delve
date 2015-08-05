package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/rpc"
	"github.com/derekparker/delve/terminal"
	"github.com/spf13/cobra"
)

const version string = "0.6.0.beta"

var (
	Log      bool
	Headless bool
	Addr     string
)

func main() {
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

	// 'version' subcommand.
	versionCommand := &cobra.Command{
		Use:   "version",
		Short: "Prints version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Delve version: " + version)
		},
	}
	rootCommand.AddCommand(versionCommand)

	// 'run' subcommand.
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Compile and begin debugging program.",
		Long: `Compiles your program with optimizations disabled, 
starts and attaches to it, and enables you to immediately begin debugging your program.`,
		Run: func(cmd *cobra.Command, args []string) {
			status := func() int {
				const debugname = "debug"
				goBuild := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
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
				return execute(0, processArgs)
			}()
			os.Exit(status)
		},
	}
	rootCommand.AddCommand(runCommand)

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
					goBuild := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
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
						if state.Err != nil {
							fmt.Fprintln(os.Stderr, state.Err)
							return 0
						}
						var args []string
						var fname string
						if state.CurrentThread != nil && state.CurrentThread.Function != nil {
							fname = state.CurrentThread.Function.Name
						}
						if state.BreakpointInfo != nil {
							for _, arg := range state.BreakpointInfo.Arguments {
								args = append(args, arg.Value)
							}
						}
						fmt.Printf("%s(%s) %s:%d\n", fname, strings.Join(args, ", "), state.CurrentThread.File, state.CurrentThread.Line)
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
				goTest := exec.Command("go", "test", "-c", "-gcflags", "-N -l")
				goTest.Stderr = os.Stderr
				err = goTest.Run()
				if err != nil {
					return 1
				}
				debugname := "./" + base + ".test"
				defer os.Remove(debugname)
				processArgs := append([]string{debugname}, args...)

				return execute(0, processArgs)
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
		Run: func(cmd *cobra.Command, args []string) {
			pid, err := strconv.Atoi(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid pid: %d", args[0])
				os.Exit(1)
			}
			os.Exit(execute(pid, nil))
		},
	}
	rootCommand.AddCommand(attachCommand)

	rootCommand.Execute()
}

func execute(attachPid int, processArgs []string) int {
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
