package native

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

const waitForHelperEnv = "DELVE_WAITFOR_HELPER"

func TestWaitForSearchPastSeenProcess(t *testing.T) {
	switch runtime.GOOS {
	case "freebsd", "linux", "windows":
		// waitForSearchProcess is only implemented on these operating systems.
	default:
		t.Skipf("not implemented on %s", runtime.GOOS)
	}

	if os.Getenv(waitForHelperEnv) == "1" {
		// Stay alive until the parent finds and kills us, with an orphan timeout.
		time.Sleep(30 * time.Second)
		return
	}

	// Populate seen with the current process snapshot.
	seen := make(map[int]struct{})
	prefix := fmt.Sprintf("delve-impossible-process-%d", os.Getpid())
	pid, err := waitForSearchProcess(prefix, seen)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 0 {
		t.Fatalf("unexpectedly found process %d", pid)
	}

	// Start a process that was not present in the initial snapshot.
	uniqueArg := fmt.Sprintf("waitfor-child-%d", os.Getpid())
	cmd := exec.Command(os.Args[0], "-test.run=^TestWaitForSearchPastSeenProcess$", uniqueArg)
	cmd.Env = append(os.Environ(), waitForHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	time.Sleep(10 * time.Millisecond)

	pid, err = waitForSearchProcess(strings.Join(cmd.Args, " "), seen)
	if err != nil {
		t.Fatal(err)
	}
	if pid != cmd.Process.Pid {
		t.Fatalf("expected PID %d, got %d", cmd.Process.Pid, pid)
	}
}
