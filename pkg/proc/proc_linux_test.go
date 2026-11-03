package proc_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func mustHaveObjcopy(t *testing.T) {
	t.Helper()
	if objcopyPath, _ := exec.LookPath("objcopy"); objcopyPath == "" {
		t.Skip("no objcopy in path")
	}
}

func TestLoadingExternalDebugInfo(t *testing.T) {
	mustHaveObjcopy(t)
	fixture := protest.BuildFixture(t, "locationsprog", 0)
	defer os.Remove(fixture.Path)
	stripAndCopyDebugInfo(fixture, t)
	p, err := native.Launch(append([]string{fixture.Path}, ""), "", 0, []string{filepath.Dir(fixture.Path)}, "", "", proc.OutputRedirect{}, proc.OutputRedirect{})
	if err != nil {
		t.Fatal(err)
	}
	p.Detach(true)
}

func TestGnuDebuglink(t *testing.T) {
	mustHaveObjcopy(t)
	// build math.go and make a copy of the executable
	fixture := protest.BuildFixture(t, "math", 0)
	buf, err := os.ReadFile(fixture.Path)
	assertNoError(err, t, "ReadFile")
	debuglinkPath := fixture.Path + "-gnu_debuglink"
	assertNoError(os.WriteFile(debuglinkPath, buf, 0666), t, "WriteFile")
	defer os.Remove(debuglinkPath)

	run := func(exe string, args ...string) {
		cmd := exec.Command(exe, args...)
		out, err := cmd.CombinedOutput()
		assertNoError(err, t, fmt.Sprintf("%s %q: %s", cmd, strings.Join(args, " "), out))
	}

	// convert the executable copy to use .gnu_debuglink
	debuglinkDwoPath := debuglinkPath + ".dwo"
	run("objcopy", "--only-keep-debug", debuglinkPath, debuglinkDwoPath)
	defer os.Remove(debuglinkDwoPath)
	run("objcopy", "--strip-debug", debuglinkPath)
	run("objcopy", "--add-gnu-debuglink="+debuglinkDwoPath, debuglinkPath)

	// open original executable
	normalBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH, false)
	assertNoError(normalBinInfo.LoadBinaryInfo(fixture.Path, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (normal exe)")

	// open .gnu_debuglink executable
	debuglinkBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH, false)
	assertNoError(debuglinkBinInfo.LoadBinaryInfo(debuglinkPath, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (gnu_debuglink exe)")

	if len(normalBinInfo.Functions) != len(debuglinkBinInfo.Functions) {
		t.Fatalf("function list mismatch")
	}

	for i := range normalBinInfo.Functions {
		normalFn := normalBinInfo.Functions[i]
		debuglinkFn := debuglinkBinInfo.Functions[i]
		if normalFn.Entry != debuglinkFn.Entry || normalFn.Name != debuglinkFn.Name {
			t.Fatalf("function definition mismatch")
		}
	}
}

// makeGnuDebuglinkCopy writes a copy of the executable src to dir with its
// debug info moved to a separate file next to it, referenced by a
// .gnu_debuglink section. It returns the path of the stripped copy.
func makeGnuDebuglinkCopy(t *testing.T, src, dir string) string {
	t.Helper()
	buf, err := os.ReadFile(src)
	assertNoError(err, t, "ReadFile")
	exe := filepath.Join(dir, filepath.Base(src))
	assertNoError(os.WriteFile(exe, buf, 0755), t, "WriteFile")

	run := func(args ...string) {
		cmd := exec.Command("objcopy", args...)
		out, err := cmd.CombinedOutput()
		assertNoError(err, t, fmt.Sprintf("%s: %s", cmd, out))
	}
	run("--only-keep-debug", exe, exe+".dwo")
	run("--strip-debug", exe)
	run("--add-gnu-debuglink="+exe+".dwo", exe)
	return exe
}

// assertSeparateDebugInfoLoaded checks that loading the stripped binary at
// path found its separate debug info file. Types are only read from DWARF,
// so there are none when the debug info file was not found.
func assertSeparateDebugInfoLoaded(t *testing.T, path string) {
	t.Helper()
	bi := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH, false)
	assertNoError(bi.LoadBinaryInfo(path, 0, []string{"/debugdir"}), t, "LoadBinaryInfo")
	types, err := bi.Types()
	assertNoError(err, t, "Types")
	if len(types) == 0 {
		t.Fatalf("no DWARF loaded through %s: separate debug info file not found", path)
	}
}

const mountNamespaceHelperEnv = "DELVE_TEST_MOUNT_NAMESPACE_HELPER"

// mountNamespaceHelperNoPerm is the exit code of the mount namespace helper
// when it is not allowed to mount, e.g. because a security policy denies
// capabilities inside unprivileged user namespaces.
const mountNamespaceHelperNoPerm = 77

// TestMountNamespaceHelper is not a test: it is the helper process started
// by TestGnuDebuglinkOtherMountNamespace inside a new mount namespace. It
// mounts a tmpfs on the directory named by its first argument, copies the
// files of the directory named by its second argument there, and executes
// the third argument in place.
func TestMountNamespaceHelper(t *testing.T) {
	if os.Getenv(mountNamespaceHelperEnv) == "" {
		t.Skip("helper process for TestGnuDebuglinkOtherMountNamespace")
	}
	args := flag.Args()
	mountDir, srcDir, exe := args[0], args[1], args[2]
	fail := func(code int, err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(code)
	}
	// Mounts would propagate back to the parent namespace if / were shared.
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		fail(mountNamespaceHelperNoPerm, fmt.Errorf("making / private: %w", err))
	}
	if err := syscall.Mount("tmpfs", mountDir, "tmpfs", 0, ""); err != nil {
		fail(mountNamespaceHelperNoPerm, fmt.Errorf("mounting tmpfs on %s: %w", mountDir, err))
	}
	if err := os.CopyFS(mountDir, os.DirFS(srcDir)); err != nil {
		fail(1, err)
	}
	fail(1, syscall.Exec(exe, []string{exe}, nil))
}

// TestGnuDebuglinkOtherMountNamespace checks that the .gnu_debuglink file
// next to the executable is found through /proc/<pid>/exe when the process
// runs in another mount namespace, as when debugging a container process
// from a sidecar or a Kubernetes ephemeral container. The executable is
// placed on a tmpfs that is only mounted inside the new namespace, so the
// path that /proc/<pid>/exe reads as does not exist in ours.
func TestGnuDebuglinkOtherMountNamespace(t *testing.T) {
	mustHaveObjcopy(t)
	fixture := protest.BuildFixture(t, "loopprog", 0)
	stagingDir := t.TempDir()
	makeGnuDebuglinkCopy(t, fixture.Path, stagingDir)
	mountDir := t.TempDir()
	exe := filepath.Join(mountDir, filepath.Base(fixture.Path))

	// The helper executes exe in place, so cmd.Process.Pid ends up running it.
	cmd := exec.Command(os.Args[0], "-test.run=^TestMountNamespaceHelper$", "--", mountDir, stagingDir, exe)
	cmd.Env = append(os.Environ(), mountNamespaceHelperEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS}
	if os.Getuid() != 0 {
		// Without root, a new user namespace is needed to create a mount
		// namespace. Map our IDs to root inside it.
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot create a mount namespace: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	defer func() {
		cmd.Process.Kill()
		<-exited
	}()

	procPath := fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid)
	deadline := time.After(10 * time.Second)
	for {
		if target, _ := os.Readlink(procPath); target == exe {
			break
		}
		select {
		case <-exited:
			if cmd.ProcessState.ExitCode() == mountNamespaceHelperNoPerm {
				t.Skipf("cannot mount in a new mount namespace: %s", stderr.String())
			}
			t.Fatalf("mount namespace helper exited (%v): %s", cmd.ProcessState, stderr.String())
		case <-deadline:
			t.Fatalf("target did not exec %s", exe)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := filepath.EvalSymlinks(procPath); err == nil {
		t.Fatalf("EvalSymlinks(%q) succeeded, test precondition not met", procPath)
	}

	assertSeparateDebugInfoLoaded(t, procPath)
}

// TestGnuDebuglinkDeletedExecutable checks that the .gnu_debuglink file next
// to the executable is found through a /proc link to an executable that was
// deleted (or replaced) after it was opened. The link reads as
// "<path> (deleted)", which filepath.EvalSymlinks cannot resolve, but the
// debug info file is still in the executable's directory.
func TestGnuDebuglinkDeletedExecutable(t *testing.T) {
	mustHaveObjcopy(t)
	fixture := protest.BuildFixture(t, "math", 0)
	debuglinkPath := makeGnuDebuglinkCopy(t, fixture.Path, t.TempDir())

	f, err := os.Open(debuglinkPath)
	assertNoError(err, t, "Open")
	defer f.Close()
	assertNoError(os.Remove(debuglinkPath), t, "Remove")
	procPath := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd())
	if _, err := filepath.EvalSymlinks(procPath); err == nil {
		t.Fatalf("EvalSymlinks(%q) succeeded, test precondition not met", procPath)
	}

	assertSeparateDebugInfoLoaded(t, procPath)
}

func stripAndCopyDebugInfo(f protest.Fixture, t *testing.T) {
	name := filepath.Base(f.Path)
	// Copy the debug information to an external file.
	copyCmd := exec.Command("objcopy", "--only-keep-debug", name, name+".debug")
	copyCmd.Dir = filepath.Dir(f.Path)
	if err := copyCmd.Run(); err != nil {
		t.Fatal(err)
	}
	protest.AddPathToRemove(f.Path + ".debug")

	// Strip the original binary of the debug information.
	stripCmd := exec.Command("strip", "--strip-debug", "--strip-unneeded", name)
	stripCmd.Dir = filepath.Dir(f.Path)
	if err := stripCmd.Run(); err != nil {
		t.Fatal(err)
	}
}
