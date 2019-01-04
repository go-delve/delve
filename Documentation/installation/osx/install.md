# Installation on OSX

Ensure you have a proper compilation toolchain.

This should be as simple as:

`xcode-select --install`

Now you can install delve using `go get`:

```
$ go get -u github.com/go-delve/delve/cmd/dlv
```

With this method you will not be able to use delve's native backend, *but you don't need it anyway*: the native backend on macOS [has known problems](https://github.com/go-delve/delve/issues/1112) on recent issues of the OS and is not currently maintained.

## Compiling the native backend

Only do this if you have a valid reason to use the native backend.

1. Run `xcode-select --install`
2. On macOS 10.14 manually install the legacy include headers by running `/Library/Developer/CommandLineTools/Packages/macOS_SDK_headers_for_macOS_10.14.pkg`
3. Clone the repo into `$GOPATH/src/github.com/go-delve/delve`
4. Run `make install` in that directory (on some versions of macOS this requires being root, the first time you run it, to install a new certificate)

The makefile will take care of creating and installing a self-signed certificate automatically.
