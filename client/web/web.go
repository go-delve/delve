package web

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/goreadline"
	"github.com/derekparker/delve/proctl"

	"github.com/gorilla/websocket"
)

const historyFile string = ".dbg_history"

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func commandsHandler(dbp *proctl.DebuggedProcess) http.HandlerFunc {
	cmds := command.DebugCommands()

	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			die(1, fmt.Sprintf("%q", err))
		}

		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				die(1, fmt.Sprintf("%q", err))
			}

			cmdstr := string(message)
			if cmdstr != "" {
				goreadline.AddHistory(cmdstr)
			}

			if err != nil {
				if err == io.EOF {
					handleExit(dbp, 0)
				}
				die(1, "Prompt for input failed.\n")
			}

			cmdstr, args := parseCommand(cmdstr)

			if cmdstr == "exit" {
				handleExit(dbp, 0)
			}

			response := []byte{}
			buffer := bytes.NewBuffer(response)

			cmd := cmds.Find(cmdstr)

			err = cmd(dbp, args...)
			if err != nil {
				fmt.Fprintf(buffer, "Command failed: %s\n", err)
			}

			err = conn.WriteMessage(messageType, response)
			if err != nil {
				die(1, fmt.Sprintf("%q", err))
			}
		}
	}
}
func Run(run bool, pid int, address string, args []string) {
	var (
		dbp *proctl.DebuggedProcess
		err error
	)

	switch {
	case run:
		const debugname = "debug"
		cmd := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
		err := cmd.Run()
		if err != nil {
			die(1, "Could not compile program:", err)
		}
		defer os.Remove(debugname)

		dbp, err = proctl.Launch(append([]string{"./" + debugname}, args...))
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbp, err = proctl.Attach(pid)
		if err != nil {
			die(1, "Could not attach to process:", err)
		}
	default:
		dbp, err = proctl.Launch(args)
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, sys.SIGINT)
	go func() {
		for _ = range ch {
			if dbp.Running() {
				dbp.RequestManualStop()
			}
		}
	}()

	http.HandleFunc("/commands", commandsHandler(dbp))
	log.Fatalf("Error: %q", http.ListenAndServe(address, nil))
}

func handleExit(dbp *proctl.DebuggedProcess, status int) {
	errno := goreadline.WriteHistoryToFile(historyFile)
	if errno != 0 {
		fmt.Println("readline:", errno)
	}

	for _, bp := range dbp.HWBreakPoints {
		if bp == nil {
			continue
		}
		if _, err := dbp.Clear(bp.Addr); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", bp.Addr, err)
		}
	}

	for pc := range dbp.BreakPoints {
		if _, err := dbp.Clear(pc); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", pc, err)
		}
	}

	fmt.Println("Detaching from process...")
	err := sys.PtraceDetach(dbp.Process.Pid)
	if err != nil {
		die(2, "Could not detach", err)
	}

	fmt.Println("Killing process", dbp.Process.Pid)

	err = dbp.Process.Kill()
	if err != nil {
		fmt.Println("Could not kill process", err)
	}

	die(status, "Hope I was of service hunting your bug!")
}

func die(status int, args ...interface{}) {
	fmt.Fprint(os.Stderr, args)
	fmt.Fprint(os.Stderr, "\n")
	os.Exit(status)
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}
