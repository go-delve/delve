//go:build linux

package debugdetect

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func detectDebuggerAttached() (bool, error) {
	// Read /proc/self/status and look for TracerPid field
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return false, fmt.Errorf("failed to read /proc/self/status: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "TracerPid:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return false, fmt.Errorf("malformed TracerPid line in /proc/self/status: %s", line)
			}
			pid, err := strconv.Atoi(fields[1])
			if err != nil {
				return false, fmt.Errorf("failed to parse TracerPid value: %w", err)
			}
			return pid != 0, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("error reading /proc/self/status: %w", err)
	}

	return false, fmt.Errorf("TracerPid field not found in /proc/self/status")
}
