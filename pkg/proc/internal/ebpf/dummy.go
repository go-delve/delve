//go:build dummy
// +build dummy

// This file is part of a workaround for `go mod vendor` which won't
// vendor C files if there are no Go files in the same directory.
//
// See https://github.com/golang/go/issues/26366

package ebpf

import (
	_ "github.com/go-delve/delve/pkg/proc/internal/ebpf/bpf"
	_ "github.com/go-delve/delve/pkg/proc/internal/ebpf/bpf/include"
)
