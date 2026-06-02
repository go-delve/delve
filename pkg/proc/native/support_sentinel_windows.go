//go:build windows && !amd64 && !(arm64 && exp.winarm64)

// This file is used to detect build on unsupported GOOS/GOARCH combinations.

package your_windows_architecture_is_not_supported_by_delve
