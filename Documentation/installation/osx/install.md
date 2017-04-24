# Installation on OSX

Please use the following steps to build and install Delve on OSX.

## Via Homebrew

If you have [HomeBrew](http://brew.sh/) installed, simply run:

```
$ brew install go-delve/delve/delve
```

## Manual install

Ensure you have a proper compilation toolchain.

This should be as simple as:

`xcode-select --install`

Now you can install delve using `go get`:

```
$ go get github.com/derekparker/delve/cmd/dlv
```

With this method you will not be able to use delve's native backend.

Alternatively, you can clone the repo and run:

```
$ make install
```

The makefile will take care of creating and installing a self-signed certificate automatically.

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).
