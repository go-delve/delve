package web

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/proctl"

	"github.com/gorilla/websocket"
)

type (
	connectionHandler struct {
		mu              sync.Mutex
		connectionCount int
	}
	replyMessage struct {
		Message string
	}
)

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	messageConnectedToDelve            = replyMessage{Message: "Conected to DLV debugger"}
	errNotATextMessage                 = replyMessage{Message: "Received message is not a text message"}
	errCommandFailed                   = replyMessage{Message: "Command failed. Message: %q"}
	errCommandResultsNotImplementedYet = replyMessage{Message: "Command results are not yet implemented"}
)

//connection limiter to one client
func (ch *connectionHandler) shouldReject(w *http.ResponseWriter) bool {
	ch.mu.Lock()
	ch.connectionCount++
	shouldReject := false
	if ch.connectionCount > 1 {
		(*w).WriteHeader(429)

		shouldReject = true
	}
	ch.mu.Unlock()
	return shouldReject
}

func commandsHandler(dbp *proctl.DebuggedProcess) http.HandlerFunc {
	cmds := command.DebugCommands()
	var connectionHandler connectionHandler

	return func(w http.ResponseWriter, r *http.Request) {
		// TODO How do we want to handle multi-program debugging? Do we?
		//reject more than one connection
		if connectionHandler.shouldReject(&w) {
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Error while upgrading connection. Message: %q", err)
			return
		}

		reply(conn, messageConnectedToDelve)

		// Generally we can recover from the errors below so we should just continue our loop
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				reply(conn, commandFailed(err))
				continue
			}

			if messageType != websocket.TextMessage {
				reply(conn, errNotATextMessage)
				continue
			}

			cmdstr := string(message)

			cmdstr, args := parseCommand(cmdstr)

			if cmdstr == "exit" {
				//TODO Handle exit better
				handleExit(dbp, 0)
			}

			replyMessage := errCommandResultsNotImplementedYet
			cmd := cmds.Find(cmdstr)

			err = cmd(dbp, args...)
			if err != nil {
				reply(conn, commandFailed(err))
				continue
			}

			reply(conn, replyMessage)
		}
	}
}
func Run(run bool, pid int, address string, args []string) {
	var (
		dbp *proctl.DebuggedProcess
		err error
	)

	// TODO Should we move this to it's own section of the connection?
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

	http.HandleFunc("/", commandsHandler(dbp))
	log.Fatalf("Error: %q", http.ListenAndServe(address, nil))
}

func handleExit(dbp *proctl.DebuggedProcess, status int) {
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

	// TODO Don't kill the process unless the user wants to
	fmt.Println("Killing process", dbp.Process.Pid)
	err = dbp.Process.Kill()
	if err != nil {
		fmt.Println("Could not kill process", err)
	}

	die(status, "Hope I was of service hunting your bug!")
}

func die(status int, args ...interface{}) {
	// TODO Change this one to not die on delve, but rather send the error back on the socket
	// TODO Add a special function / command to actually stop delve when running in this mode
	fmt.Fprint(os.Stderr, args)
	fmt.Fprint(os.Stderr, "\n")
	os.Exit(status)
}

func reply(conn *websocket.Conn, reply replyMessage) {
	err := conn.WriteJSON(reply)
	if err != nil {
		log.Printf("Could not write reply to client. Error: %q. Original message: %q", err, reply)
	}
}

func commandFailed(err error) replyMessage {
	reply := errCommandFailed
	reply.Message = fmt.Sprintf(reply.Message, "Command failed: %s\n", err)

	return reply
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}
