// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//go:build darwin && !amd64 && !arm64
// +build darwin,!amd64,!arm64

package your_darwin_architectur_is_not_supported_by_delve
