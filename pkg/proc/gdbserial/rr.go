package gdbserial

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/proc"
)

// RecordAsync configures rr to record the execution of the specified
// program. Returns a run function which will actually record the program, a
// stop function which will prematurely terminate the recording of the
// program.
func RecordAsync(cmd []string, wd string, quiet bool, stdin string, stdout proc.OutputRedirect, stderr proc.OutputRedirect) (run func() (string, error), stop func() error, err error) {
	if err := checkRRAvailable(); err != nil {
		return nil, nil, err
	}

	rfd, wfd, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	args := make([]string, 0, len(cmd)+2)
	args = append(args, "record", "--print-trace-dir=3")
	args = append(args, cmd...)
	rrcmd := exec.Command("rr", args...)
	var closefn func()
	rrcmd.Stdin, rrcmd.Stdout, rrcmd.Stderr, closefn, err = openRedirects(stdin, stdout, stderr, quiet)
	if err != nil {
		return nil, nil, err
	}
	rrcmd.ExtraFiles = []*os.File{wfd}
	rrcmd.Dir = wd

	tracedirChan := make(chan string)
	go func() {
		bs, _ := io.ReadAll(rfd)
		tracedirChan <- strings.TrimSpace(string(bs))
	}()

	run = func() (string, error) {
		err := rrcmd.Run()
		closefn()
		_ = wfd.Close()
		tracedir := <-tracedirChan
		return tracedir, err
	}

	stop = func() error {
		return rrcmd.Process.Signal(syscall.SIGTERM)
	}

	return run, stop, nil
}

func openRedirects(stdinPath string, stdoutOR proc.OutputRedirect, stderrOR proc.OutputRedirect, quiet bool) (stdin, stdout, stderr *os.File, closefn func(), err error) {
	toclose := []*os.File{}

	if stdinPath != "" {
		stdin, err = os.Open(stdinPath)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		toclose = append(toclose, stdin)
	} else {
		stdin = os.Stdin
	}

	create := func(redirect proc.OutputRedirect, dflt *os.File) (f *os.File) {
		if redirect.Path != "" {
			f, err = os.Create(redirect.Path)
			if f != nil {
				toclose = append(toclose, f)
			}

			return f
		} else if redirect.File != nil {
			toclose = append(toclose, redirect.File)

			return redirect.File
		}

		if quiet {
			return nil
		}

		return dflt
	}

	stdout = create(stdoutOR, os.Stdout)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	stderr = create(stderrOR, os.Stderr)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	closefn = func() {
		for _, f := range toclose {
			_ = f.Close()
		}
	}

	return stdin, stdout, stderr, closefn, nil
}

// Record uses rr to record the execution of the specified program and
// returns the trace directory's path.
func Record(cmd []string, wd string, quiet bool, stdin string, stdout proc.OutputRedirect, stderr proc.OutputRedirect) (tracedir string, err error) {
	run, _, err := RecordAsync(cmd, wd, quiet, stdin, stdout, stderr)
	if err != nil {
		return "", err
	}

	// ignore run errors, it could be the program crashing
	return run()
}

// Replay starts an instance of rr in replay mode, with the specified trace
// directory, and connects to it.
func Replay(tracedir string, quiet, deleteOnDetach bool, debugInfoDirs []string, rrOnProcessPid int, cmdline string) (*proc.TargetGroup, error) {
	if err := checkRRAvailable(); err != nil {
		return nil, err
	}

	args := []string{
		"replay",
		"--dbgport=0",
	}
	if rrOnProcessPid != 0 {
		args = append(args, fmt.Sprintf("--onprocess=%d", rrOnProcessPid))
	}
	args = append(args, tracedir)

	rrcmd := exec.Command("rr", args...)
	rrcmd.Stdout = os.Stdout
	stderr, err := rrcmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	rrcmd.SysProcAttr = sysProcAttr(false)

	initch := make(chan rrInit)
	go rrStderrParser(stderr, initch, quiet)

	err = rrcmd.Start()
	if err != nil {
		return nil, err
	}

	init := <-initch
	if init.err != nil {
		rrcmd.Process.Kill()
		return nil, init.err
	}

	p := newProcess(rrcmd.Process)
	p.tracedir = tracedir
	p.conn.useXcmd = true // 'rr' does not support the 'M' command which is what we would usually use to write memory, this is only important during function calls, in any other situation writing memory will fail anyway.
	if deleteOnDetach {
		p.onDetach = func() {
			safeRemoveAll(p.tracedir)
		}
	}
	tgt, err := p.Dial(init.port, init.exe, cmdline, 0, debugInfoDirs, proc.StopLaunched)
	if err != nil {
		rrcmd.Process.Kill()
		return nil, err
	}

	return tgt, nil
}

// ErrPerfEventParanoid is the error returned by Reply and Record if
// /proc/sys/kernel/perf_event_paranoid is greater than 1.
type ErrPerfEventParanoid struct {
	actual int
}

func (err ErrPerfEventParanoid) Error() string {
	return fmt.Sprintf("rr needs /proc/sys/kernel/perf_event_paranoid <= 1, but it is %d", err.actual)
}

func checkRRAvailable() error {
	if _, err := exec.LookPath("rr"); err != nil {
		return &ErrBackendUnavailable{}
	}

	// Check that /proc/sys/kernel/perf_event_paranoid doesn't exist or is <= 1.
	buf, err := os.ReadFile("/proc/sys/kernel/perf_event_paranoid")
	if err == nil {
		perfEventParanoid, _ := strconv.Atoi(strings.TrimSpace(string(buf)))
		if perfEventParanoid > 1 {
			return ErrPerfEventParanoid{perfEventParanoid}
		}
	}

	return nil
}

type rrInit struct {
	port string
	exe  string
	err  error
}

const (
	rrGdbCommandPrefix = "  gdb "
	rrGdbLaunchPrefix  = "Launch gdb with"
	targetCmd          = "target extended-remote "
)

func rrStderrParser(stderr io.ReadCloser, initch chan<- rrInit, quiet bool) {
	rd := bufio.NewReader(stderr)
	defer stderr.Close()

	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			initch <- rrInit{"", "", err}
			close(initch)
			return
		}

		if strings.HasPrefix(line, rrGdbCommandPrefix) {
			initch <- rrParseGdbCommand(line[len(rrGdbCommandPrefix):])
			close(initch)
			break
		}

		if strings.HasPrefix(line, rrGdbLaunchPrefix) {
			continue
		}

		if !quiet {
			os.Stderr.Write([]byte(line))
		}
	}

	io.Copy(os.Stderr, rd)
}

type ErrMalformedRRGdbCommand struct {
	line, reason string
}

func (err *ErrMalformedRRGdbCommand) Error() string {
	return fmt.Sprintf("malformed gdb command %q: %s", err.line, err.reason)
}

func rrParseGdbCommand(line string) rrInit {
	port := ""
	fields := config.SplitQuotedFields(line, '\'')
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "-ex":
			if i+1 >= len(fields) {
				return rrInit{err: &ErrMalformedRRGdbCommand{line, "-ex not followed by an argument"}}
			}
			arg := fields[i+1]

			if !strings.HasPrefix(arg, targetCmd) {
				continue
			}

			port = arg[len(targetCmd):]
			i++

		case "-l":
			// skip argument
			i++
		}
	}

	if port == "" {
		return rrInit{err: &ErrMalformedRRGdbCommand{line, "could not find -ex argument"}}
	}

	exe := fields[len(fields)-1]

	return rrInit{port: port, exe: exe}
}

// RecordAndReplay acts like calling Record and then Replay.
func RecordAndReplay(cmd []string, wd string, quiet bool, debugInfoDirs []string, stdin string, stdout proc.OutputRedirect, stderr proc.OutputRedirect) (*proc.TargetGroup, string, error) {
	tracedir, err := Record(cmd, wd, quiet, stdin, stdout, stderr)
	if tracedir == "" {
		return nil, "", err
	}
	t, err := Replay(tracedir, quiet, true, debugInfoDirs, 0, strings.Join(cmd, " "))
	return t, tracedir, err
}

// safeRemoveAll removes dir and its contents but only as long as dir does
// not contain directories.
func safeRemoveAll(dir string) {
	fis, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, fi := range fis {
		if fi.IsDir() {
			return
		}
	}
	for _, fi := range fis {
		if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil {
			return
		}
	}
	os.Remove(dir)
}
