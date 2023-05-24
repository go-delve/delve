#!/bin/bash
set -e
set -x

apt-get -qq update
apt-get install -y dwz wget make git gcc curl jq lsof

dwz --version

version=$1
arch=$2

function getgo {
	export GOROOT=/usr/local/go/$1
	if [ ! -d "$GOROOT" ]; then
		wget -q https://dl.google.com/go/"$1".linux-"${arch}".tar.gz
		mkdir -p /usr/local/go
		tar -C /usr/local/go -xzf "$1".linux-"${arch}".tar.gz
		mv -f /usr/local/go/go "$GOROOT"
	fi
}

if [ "$version" = "gotip" ]; then
	echo Building Go from tip
	getgo $(curl https://go.dev/VERSION?m=text)
	export GOROOT_BOOTSTRAP=$GOROOT
	export GOROOT=/usr/local/go/go-tip
	git clone https://go.googlesource.com/go /usr/local/go/go-tip
	cd /usr/local/go/go-tip/src
	./make.bash
	cd -
else
	echo Finding latest patch version for $version
	echo "Go $version on $arch"
	version=$(curl 'https://go.dev/dl/?mode=json&include=all' | jq '.[].version' --raw-output | egrep ^$version'($|\.|beta|rc)' | sort -rV | head -1)
	if [ "x$version" = "x" ]; then
		version=$(curl 'https://go.dev/dl/?mode=json&include=all' | jq '.[].version' --raw-output | egrep ^$version'($|\.)' | sort -rV | head -1)
	fi
	getgo $version
fi


GOPATH=$(pwd)/go
export GOPATH
export PATH=$PATH:$GOROOT/bin:$GOPATH/bin
go version
go install honnef.co/go/tools/cmd/staticcheck@2023.1 || true

uname -a
echo "$PATH"
echo "$GOROOT"
echo "$GOPATH"
cd delve

# Starting with go1.18 'go build' and 'go run' will try to stamp the build
# with the current VCS revision, which does not work with TeamCity
if [ "$version" = "gotip" ]; then
	export GOFLAGS=-buildvcs=false
elif [ ${version:4:2} -gt 17 ]; then
	export GOFLAGS=-buildvcs=false
fi

if [ "$arch" = "386" ]; then
	ver=$(go version)
	if [ "$ver" = "go version go1.19 linux/386" ]; then
		export CGO_CFLAGS='-g -O0 -fno-stack-protector'
	fi
fi

set +e
make test
x=$?
if [ "$version" = "gotip" ]; then
	exit 0
else
	exit $x
fi

