package debugdetect

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/rpc2"
)

func scanForListenAddr(t *testing.T, scanner *bufio.Scanner, stderr *bytes.Buffer) string {
	t.Helper()
	const marker = " server listening at: "
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, marker); idx >= 0 {
			return line[idx+len(marker):]
		}
	}
	t.Fatalf("dlv exited without printing listen address (stderr: %s)", stderr.String())
	return ""
}

func TestIntegration_NotAttached(t *testing.T) {
	// Build the fixture
	fixturesDir := protest.FindFixturesDir()
	fixtureSrc := filepath.Join(fixturesDir, "debugdetect.go")
	fixtureBin := filepath.Join(t.TempDir(), "debugdetect")
	if runtime.GOOS == "windows" {
		fixtureBin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", fixtureBin, fixtureSrc)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build fixture: %v\n%s", err, out)
	}

	// Run the fixture (not under debugger)
	cmd = exec.Command(fixtureBin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "NOT_ATTACHED") {
		t.Errorf("expected 'NOT_ATTACHED' in output, got: %s", output)
	}
}

func TestIntegration_WaitForDebugger(t *testing.T) {
	// This test verifies that WaitForDebugger() blocks until a debugger
	// attaches and then returns successfully by running the fixture
	// under Delve.
	protest.AllowRecording(t)

	dlvbin := protest.GetDlvBinary(t)
	fixturesDir := protest.FindFixturesDir()
	fixtureSrc := filepath.Join(fixturesDir, "waitfordebugger.go")

	cmd := exec.Command(dlvbin, "debug", fixtureSrc, "--headless", "--continue", "--accept-multiclient", "--listen", "127.0.0.1:0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(stdout)
	listenAddr := scanForListenAddr(t, scanner, &stderr)

	foundOutput := false
	for scanner.Scan() {
		line := scanner.Text()
		t.Log(line)
		if strings.Contains(line, "DEBUGGER_FOUND") {
			foundOutput = true
			break
		}
	}

	// Clean up - connect and detach
	client := rpc2.NewClient(listenAddr)
	client.Detach(true)
	cmd.Wait()

	if !foundOutput {
		t.Error("expected 'DEBUGGER_FOUND' in output when running under debugger")
	}
}

func TestIntegration_Attached(t *testing.T) {
	// This test verifies that IsDebuggerAttached() returns true when
	// the process is actually running under a debugger by using
	// Delve to debug the fixture source file
	protest.AllowRecording(t)

	dlvbin := protest.GetDlvBinary(t)
	fixturesDir := protest.FindFixturesDir()
	fixtureSrc := filepath.Join(fixturesDir, "debugdetect.go")

	// Run the fixture under dlv debug with --headless --continue
	// This will attach the debugger, compile and run the program
	cmd := exec.Command(dlvbin, "debug", fixtureSrc, "--headless", "--continue", "--accept-multiclient", "--listen", "127.0.0.1:0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(stdout)
	listenAddr := scanForListenAddr(t, scanner, &stderr)

	foundOutput := false
	for scanner.Scan() {
		line := scanner.Text()
		t.Log(line)
		if strings.Contains(line, "ATTACHED") {
			foundOutput = true
			break
		}
	}

	// Clean up - connect and detach
	client := rpc2.NewClient(listenAddr)
	client.Detach(true)
	cmd.Wait()

	if !foundOutput {
		t.Error("expected 'ATTACHED' in output when running under debugger")
	}
}
