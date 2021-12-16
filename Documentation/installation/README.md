# Installation
The following instructions are known to work on Linux, macOS, Windows and FreeBSD.

Clone the git repository and build:

```
$ git clone https://github.com/go-delve/delve
$ cd delve
$ go install github.com/go-delve/delve/cmd/dlv
```

Alternatively, on Go version 1.16 or later:

```
# Install the latest release:
$ go install github.com/go-delve/delve/cmd/dlv@latest

# Install at tree head:
$ go install github.com/go-delve/delve/cmd/dlv@master

# Install at a specific version or pseudo-version:
$ go install github.com/go-delve/delve/cmd/dlv@v1.7.3
$ go install github.com/go-delve/delve/cmd/dlv@v1.7.4-0.20211208103735-2f13672765fe
```
See [Versions](https://go.dev/ref/mod#versions) and [Pseudo-versions](https://go.dev/ref/mod#pseudo-versions) for how to format the version suffixes.

See `go help install` for details on where the `dlv` executable is saved.

If during the install step you receive an error similar to this:

```
found packages native (proc.go) and your_operating_system_and_architecture_combination_is_not_supported_by_delve (support_sentinel.go) in /home/pi/go/src/github.com/go-delve/delve/pkg/proc/native
```

It means that your combination of operating system and CPU architecture is not supported, check the output of `go version`.

## macOS considerations

On macOS make sure you also install the command line developer tools:

```
$ xcode-select --install
```

If you didn't enable Developer Mode using Xcode you will be asked to authorize the debugger every time you use it. To enable Developer Mode and only have to authorize once per session use:

```
sudo /usr/sbin/DevToolsSecurity -enable
```

You might also need to add your user to the developer group:

```
sudo dscl . append /Groups/_developer GroupMembership $(whoami)
```

## Compiling macOS native backend

You do not need the macOS native backend and it [has known problems](https://github.com/go-delve/delve/issues/1112). If you still want to build it:

1. Run `xcode-select --install`
2. On macOS 10.14 manually install the legacy include headers by running `/Library/Developer/CommandLineTools/Packages/macOS_SDK_headers_for_macOS_10.14.pkg`
3. Clone the repo into `$GOPATH/src/github.com/go-delve/delve`
4. Run `make install` in that directory (on some versions of macOS this requires being root, the first time you run it, to install a new certificate)

The makefile will take care of creating and installing a self-signed certificate automatically.
