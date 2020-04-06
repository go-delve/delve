# Installation on FreeBSD

Please use the following steps to build and install Delve on FreeBSD.

There are two ways to install on FreeBSD. First is the standard `go get` method:

```
go get github.com/go-delve/delve/cmd/dlv
```

Note: if you are using Go in modules mode you must execute this command outside of a module directory or Delve will be added to your project as a dependency.

Alternatively make sure $GOPATH is set (e.g. as `~/.go`) and:

```
$ git clone https://github.com/go-delve/delve.git $GOPATH/src/github.com/go-delve/delve
$ cd $GOPATH/src/github.com/go-delve/delve
$ gmake install
```

