// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//go:build windows && !amd64 && !(arm64 && exp.winarm64)
// +build windows
// +build !amd64
// +build !arm64 !exp.winarm64

package your_windows_architecture_is_not_supported_by_delve
