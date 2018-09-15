# Installation on OSX

Please use the following steps to build and install Delve on OSX.

## Manual install

Ensure you have a proper compilation toolchain.

This should be as simple as:

`xcode-select --install`

Now you can install delve using `go get`:

```
$ go get -u github.com/derekparker/delve/cmd/dlv
```

With this method you will not be able to use delve's native backend.

Alternatively, you can clone the repo into `$GOPATH/github.com/derekparker/delve` and run:

```
$ make install
```
from that directory.

The makefile will take care of creating and installing a self-signed certificate automatically.

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).
