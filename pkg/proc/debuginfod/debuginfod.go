package debuginfod

import (
	"os"
	"os/exec"
	"strings"
)

const (
	debuginfodFind       = "debuginfod-find"
	debuginfodMaxtimeEnv = "DEBUGINFOD_MAXTIME"
	debuginfodTimeoutEnv = "DEBUGINFOD_TIMEOUT"
)

func execFind(args ...string) (string, error) {
	if _, err := exec.LookPath(debuginfodFind); err != nil {
		return "", err
	}
	cmd := exec.Command(debuginfodFind, args...)
	if os.Getenv(debuginfodMaxtimeEnv) == "" || os.Getenv(debuginfodTimeoutEnv) == "" {
		cmd.Env = append(os.Environ(), debuginfodMaxtimeEnv+"=1", debuginfodTimeoutEnv+"=1")
	}
	cmd.Stderr = os.Stderr
	out, err := cmd.Output() // ignore stderr
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), err
}

func GetSource(buildid, filename string) (string, error) {
	return execFind("source", buildid, filename)
}

func GetDebuginfo(buildid string) (string, error) {
	return execFind("debuginfo", buildid)
}
