package debuginfod

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strings"
	"time"
)

const debuginfodFind = "debuginfod-find"
const notificationThrottle time.Duration = 1 * time.Second

func execFind(notify func(string), args ...string) (string, error) {
	if _, err := exec.LookPath(debuginfodFind); err != nil {
		return "", err
	}
	cmd := exec.Command(debuginfodFind, args...)
	if notify != nil {
		cmd.Env = append(os.Environ(), "DEBUGINFOD_PROGRESS=yes")
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return "", err
		}
		s := bufio.NewScanner(stderr)
		s.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
			if atEOF && len(data) == 0 {
				return 0, nil, nil
			}
			if i := bytes.IndexAny(data, "\n\r"); i >= 0 {
				return i + 1, dropCR(data[0:i]), nil
			}
			if atEOF {
				return len(data), dropCR(data), nil
			}
			return 0, nil, nil
		})
		go func() {
			var tlast time.Time
			for s.Scan() {
				if time.Since(tlast) > notificationThrottle {
					tlast = time.Now()
					notify(string(s.Text()))
				}
			}
		}()
	}
	out, err := cmd.Output() // ignore stderr
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), err
}

func dropCR(data []byte) []byte {
	r, _ := bytes.CutSuffix(data, []byte{'\r'})
	return r
}

func GetSource(buildid, filename string) (string, error) {
	return execFind(nil, "source", buildid, filename)
}

func GetDebuginfo(notify func(string), buildid string) (string, error) {
	return execFind(notify, "debuginfo", buildid)
}
