// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//go:build freebsd && !amd64
// +build freebsd,!amd64

package your_freebsd_architecture_is_not_supported_by_delve
