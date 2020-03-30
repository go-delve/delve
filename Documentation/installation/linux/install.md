# Installation on Linux

Please use the following steps to build and install Delve on Linux.
(Warning, this does not support wsl 1. If you are using wsl, please update you wsl)

There are two ways to install on Linux. First is the standard `go get` method:

```
go get -u github.com/go-delve/delve/cmd/dlv
```

Alternatively make sure $GOPATH is set (e.g. as `~/.go`) and:

```
$ git clone https://github.com/go-delve/delve.git $GOPATH/src/github.com/go-delve/delve
$ cd $GOPATH/src/github.com/go-delve/delve
$ make install
```

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).
