package helpers

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func checkEnvPath(env string) (string, error) {
	filePath, _ := os.LookupEnv(env)
	if filePath != "" {
		_, err := os.Stat(filePath)
		if err != nil {
			return "", fmt.Errorf("could not open %s %s", env, filePath)
		}
		return filePath, nil
	}
	return "", nil
}

// UnameRelease gets the version string of the current running kernel
func UnameRelease() (string, error) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		return "", fmt.Errorf("could not get utsname")
	}

	var buf [65]byte
	for i, b := range uname.Release {
		buf[i] = byte(b)
	}

	ver := string(buf[:])
	ver = strings.Trim(ver, "\x00")

	return ver, nil
}

// CompareKernelRelease will compare two given kernel version/release
// strings and return -1, 0 or 1 if given version is less, equal or bigger,
// respectively, than the given one
//
// Examples of $(uname -r):
//
// 5.11.0-31-generic (ubuntu)
// 4.18.0-305.12.1.el8_4.x86_64 (alma)
// 4.18.0-338.el8.x86_64 (stream8)
// 4.18.0-305.7.1.el8_4.centos.x86_64 (centos)
// 4.18.0-305.7.1.el8_4.centos.plus.x86_64 (centos + plus repo)
// 5.13.13-arch1-1 (archlinux)
//
func CompareKernelRelease(base, given string) int {
	b := strings.Split(base, "-") // [base]-xxx
	b = strings.Split(b[0], ".")  // [major][minor][patch]

	g := strings.Split(given, "-")
	g = strings.Split(g[0], ".")

	for n := 0; n <= 2; n++ {
		i, _ := strconv.Atoi(g[n])
		j, _ := strconv.Atoi(b[n])

		if i > j {
			return 1 // given is bigger
		} else if i < j {
			return -1 // given is less
		} else {
			continue // equal
		}
	}

	return 0 // equal
}
