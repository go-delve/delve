package common

import (
	"fmt"
	"os"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/proctl"
)

func ParseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func HandleExit(dbp *proctl.DebuggedProcess, withKill bool) {
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
		Die(2, "Could not detach", err)
	}

	if withKill {
		fmt.Println("Killing process", dbp.Process.Pid)
		err = dbp.Process.Kill()
		if err != nil {
			fmt.Println("Could not kill process", err)
		}
	}
}

func Die(status int, args ...interface{}) {
	// TODO Change this one to not die on delve, but rather send the error back on the socket
	// TODO Add a special function / command to actually stop delve when running in web mode
	fmt.Fprint(os.Stderr, args)
	fmt.Fprint(os.Stderr, "\n")
	os.Exit(status)
}
