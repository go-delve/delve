# Installation on Linux

Please use the following steps to build and install Delve on Linux.

There are two ways to install on Linux. First is the standard `go get` method:
```
go get -u github.com/derekparker/delve/cmd/dlv
```
Note that there's a `go.mod` in Delve codebase. To use install using go modules `GO111MODULE=on` environment variable needs to be set before issuing `go get` while go modules are not turned on by default in the go ditribution.

Alternatively make sure $GOPATH is set (e.g. as `~/.go`) and:

```
$ git clone https://github.com/derekparker/delve.git $GOPATH/src/github.com/derekparker/delve
$ cd $GOPATH/src/github.com/derekparker/delve
$ make install
```

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).
