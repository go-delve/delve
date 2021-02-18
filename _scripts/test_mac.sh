#!/bin/bash

set -x
set -e

GOVERSION=$1
ARCH=$2
TMPDIR=$3

if [ "$GOVERSION" = "gotip" ]; then
    git clone https://go.googlesource.com/go $TMPDIR/go
    export GOROOT_BOOTSTRAP=$GOROOT
    cd $TMPDIR/go/src
    ./make.bash
    cd -
else
    cd $TMPDIR
    curl -sSL "https://storage.googleapis.com/golang/go$GOVERSION.darwin-$ARCH.tar.gz" | tar -vxz
    cd -
fi

mkdir -p $TMPDIR/gopath

export GOROOT="$TMPDIR/go"
export GOPATH="$TMPDIR/gopath"
export GOARCH="$ARCH"
export PATH="$GOROOT/bin:$PATH"

make test
