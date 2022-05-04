package debuginfod

import (
	"os/exec"
	"strings"
)

const debuginfodFind = "debuginfod-find"

func execFind(args ...string) (string, error) {
	if _, err := exec.LookPath(debuginfodFind); err != nil {
		return "", err
	}
	cmd := exec.Command(debuginfodFind, args...)
	out, err := cmd.CombinedOutput()
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
