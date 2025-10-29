#!/bin/bash
cl() {
	echo $1/$2
	capslock -goos $1 -goarch $2 -packages ./cmd/dlv > _scripts/capslock_$1_$2-output.txt
}
cl darwin amd64
cl darwin arm64
cl linux 386
cl linux amd64
cl linux arm64
cl linux ppc64le
cl linux riscv64
cl windows amd64
cl windows arm64
