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
	exit 0
	echo Building Go from tip
	getgo $(curl https://golang.org/VERSION?m=text)
	export GOROOT_BOOTSTRAP=$GOROOT
	export GOROOT=/usr/local/go/go-tip
	git clone https://go.googlesource.com/go /usr/local/go/go-tip
	cd /usr/local/go/go-tip/src
	./make.bash
	cd -
else
	echo Finding latest patch version for $version
	version=$(curl 'https://golang.org/dl/?mode=json&include=all' | jq '.[].version' --raw-output | egrep ^$version'($|\.|beta|rc)' | head -1)
	echo "Go $version on $arch"
	getgo $version
fi


GOPATH=$(pwd)/go
export GOPATH
export PATH=$PATH:$GOROOT/bin:$GOPATH/bin
go version

uname -a
echo "$PATH"
echo "$GOROOT"
echo "$GOPATH"
cd delve
make test
