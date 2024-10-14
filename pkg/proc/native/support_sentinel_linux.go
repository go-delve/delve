//go:build linux && !amd64 && !arm64 && !386 && !(ppc64le && exp.linuxppc64le) && !(riscv64 && exp.linuxriscv64)

// This file is used to detect build on unsupported GOOS/GOARCH combinations.

package your_linux_architecture_is_not_supported_by_delve
