# Installation on Windows

Please use the following steps to build and install Delve on Windows.

If you have a unix-y shell on Windows ([MSYS2](http://sourceforge.net/p/msys2/wiki/MSYS2%20installation/), [CYGWIN](https://cygwin.com/install.html) or other), follow the Linux installation directions.  

From a standard Windows `cmd` shell, use the following steps:

## 1) Install MinGW

Install [MinGW-W64](http://sourceforge.net/projects/mingw-w64/) to get a GCC toolchain which is required to [build with CGO on Windows](https://github.com/golang/go/wiki/cgo#windows).

You should select:

* Version: Latest available (`5.3.0` at time of writing)
* Architecture: `x86_64`
* Threads: `posix` (shouldn't actually matter)
* Exception: `seh` (shouldn't actually matter)
* Build revision: Latest available (`0` at time of writing)


## 2) Make and install the `dlv` binary
```
$ mingw32-make install
```

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).
