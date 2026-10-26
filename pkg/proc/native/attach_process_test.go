//go:build linux || windows || (darwin && macnative)

package native_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestAttachWithPathMissingExecutableLeavesProcessAlive(t *testing.T) {
	fixture := protest.BuildFixture(t, "loopprog", 0)
	cmd := exec.Command(fixture.Path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var exited chan error
	defer func() {
		_ = cmd.Process.Kill()
		if exited == nil {
			_ = cmd.Wait()
			return
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("target did not exit after cleanup")
		}
	}()

	group, err := native.AttachWithPath(cmd.Process.Pid, filepath.Join(t.TempDir(), "missing"), nil, nil)
	if group != nil {
		_ = group.Detach(false)
	}
	var binaryErr *proc.ErrBadBinaryInfo
	if !errors.As(err, &binaryErr) || !errors.Is(binaryErr.Err, os.ErrNotExist) {
		t.Fatalf("expected missing executable error, got %v", err)
	}
	// Do not race the debugger's wait operations while it owns the process.
	exited = make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		// Return the result so cleanup also observes the already exited child.
		exited <- err
		t.Fatalf("failed attach killed target: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}
