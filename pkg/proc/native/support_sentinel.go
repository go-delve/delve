// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//+build !linux,!darwin,!windows,!freebsd linux,!amd64,!arm64,!amd64 windows,!amd64 freebsd,!amd64

package your_operating_system_and_architecture_combination_is_not_supported_by_delve
