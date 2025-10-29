//go:build linux && !amd64 && !arm64 && !386 && !riscv64 && !(loong64 && exp.linuxloong64) && !ppc64le

// This file is used to detect build on unsupported GOOS/GOARCH combinations.

package your_linux_architecture_is_not_supported_by_delve
