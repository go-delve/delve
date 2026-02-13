package debugdetect

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/rpc2"
)

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
	const listenAddr = "127.0.0.1:40580"
	cmd := exec.Command(dlvbin, "debug", fixtureSrc, "--headless", "--continue", "--accept-multiclient", "--listen", listenAddr)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Read stdout until we see the program output
	scanner := bufio.NewScanner(stdout)
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
