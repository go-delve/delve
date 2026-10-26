package debugger

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestDebuggerAttachExecutablePath(t *testing.T) {
	fixture := protest.BuildFixture(t, "loopprog", 0)
	for _, backend := range []string{"native", "default"} {
		for _, scenario := range []string{"empty", "explicit", "proc-root", "missing"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				cmd := exec.Command(fixture.Path)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}()
				path := ""
				switch scenario {
				case "explicit":
					path = fixture.Path
				case "proc-root":
					// This exercises the path spelling, not a separate mount namespace.
					path = fmt.Sprintf("/proc/%d/root%s", cmd.Process.Pid, fixture.Path)
				case "missing":
					path = filepath.Join(t.TempDir(), "missing-executable")
				}
				d := &Debugger{config: &Config{Backend: backend}}
				group, err := d.Attach(cmd.Process.Pid, path, nil)
				if group != nil {
					defer func() {
						if err := group.Detach(false); err != nil {
							t.Error(err)
						}
					}()
				}
				if scenario == "missing" {
					var binaryErr *proc.ErrBadBinaryInfo
					if !errors.As(err, &binaryErr) || !errors.Is(binaryErr.Err, os.ErrNotExist) {
						t.Fatalf("expected missing executable error for %q, got %v", path, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := path
				if want == "" {
					want = fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid)
				}
				if got := group.Selected.BinInfo().Images[0].Path; got != want {
					t.Fatalf("executable path = %q, want %q", got, want)
				}
			})
		}
	}
}
