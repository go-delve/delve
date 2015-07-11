package main

import (
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
	}
	rootCommand.Flags().StringVarP(&Addr, "listen", "l", "localhost:0", "Debugging server listen address.")
	rootCommand.Flags().BoolVarP(&Log, "log", "", false, "Enable debugging server logging.")
	rootCommand.Flags().BoolVarP(&Headless, "headless", "", false, "Run debug server only, in headless mode.")

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
starts and attaches to it, and enable you to immediately begin debugging your program.`,
		Run: func(cmd *cobra.Command, args []string) {
			const debugname = "debug"
			goBuild := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
			goBuild.Stderr = os.Stderr
			err := goBuild.Run()
			if err != nil {
				os.Exit(1)
			}
			fp, err := filepath.Abs("./" + debugname)
			if err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}

			processArgs := append([]string{"./" + debugname}, args...)
			status := execute(0, processArgs)
			os.Remove(fp)
			os.Exit(status)
		},
	}
	rootCommand.AddCommand(runCommand)

	// 'test' subcommand.
	testCommand := &cobra.Command{
		Use:   "test",
		Short: "Compile test binary and begin debugging program.",
		Long: `Compiles a test binary with optimizations disabled, 
starts and attaches to it, and enable you to immediately begin debugging your program.`,
		Run: func(cmd *cobra.Command, args []string) {
			wd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}
			base := filepath.Base(wd)
			goTest := exec.Command("go", "test", "-c", "-gcflags", "-N -l")
			goTest.Stderr = os.Stderr
			err = goTest.Run()
			if err != nil {
				os.Exit(1)
			}
			debugname := "./" + base + ".test"
			processArgs := append([]string{debugname}, args...)

			status := execute(0, processArgs)
			os.Remove(debugname)
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
	var server service.Server
	server = rpc.NewServer(&service.Config{
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
