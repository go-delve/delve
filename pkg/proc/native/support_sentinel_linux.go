// This file is used to detect build on unsupported GOOS/GOARCH combinations.

//go:build linux && !amd64 && !arm64 && !386 && !(ppc64le && exp.linuxppc64le)
// +build linux
// +build !amd64
// +build !arm64
// +build !386
// +build !ppc64le !exp.linuxppc64le

package your_linux_architecture_is_not_supported_by_delve
