#!/bin/bash

set -x
set -e

GOVERSION=$1
ARCH=$2
TMPDIR=$3

if [ "$GOVERSION" = "gotip" ]; then
    #exit 0
    bootstrapver=$(curl https://golang.org/VERSION?m=text)
    cd $TMPDIR
    curl -sSL "https://storage.googleapis.com/golang/$bootstrapver.darwin-$ARCH.tar.gz" | tar -xz
    cd -
    if [ -x $TMPDIR/go-tip ]; then
    	cd $TMPDIR/go-tip
    	git pull origin
    else
    	git clone https://go.googlesource.com/go $TMPDIR/go-tip
    fi
    export GOROOT_BOOTSTRAP=$TMPDIR/go
    export GOROOT=$TMPDIR/go-tip
    cd $TMPDIR/go-tip/src
    ./make.bash
    cd -
else
    echo Finding latest patch version for $GOVERSION
    GOVERSION=$(python _scripts/latestver.py $GOVERSION)
    echo Go $GOVERSION on $ARCH
    cd $TMPDIR
    curl -sSL "https://storage.googleapis.com/golang/$GOVERSION.darwin-$ARCH.tar.gz" | tar -xz
    cd -
    export GOROOT="$TMPDIR/go"
fi

mkdir -p $TMPDIR/gopath

export GOPATH="$TMPDIR/gopath"
export GOARCH="$ARCH"
export PATH="$GOROOT/bin:$PATH"
go version

make test
