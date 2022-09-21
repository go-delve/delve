// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//go:build !linux && !darwin && !windows && !freebsd
// +build !linux,!darwin,!windows,!freebsd

package your_operating_system_is_not_supported_by_delve
