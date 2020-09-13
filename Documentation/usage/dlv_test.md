## dlv test

Compile test binary and begin debugging program.

### Synopsis


Compiles a test binary with optimizations disabled and begins a new debug session.

The test command allows you to begin a new debug session in the context of your
unit tests. By default Delve will debug the tests in the current directory.
Alternatively you can specify a package name, and Delve will debug the tests in
that package instead.

```
dlv test [package]
```

### Options

```
      --output string   Output path for the binary. (default "debug.test")
```

### Options inherited from parent commands

```
      --accept-multiclient               Allows a headless server to accept multiple client connections.
      --allow-non-terminal-interactive   Allows interactive sessions of Delve that don't have a terminal as stdin, stdout and stderr
      --api-version int                  Selects API version when headless. New clients should use v2. Can be reset via RPCServer.SetApiVersion. See Documentation/api/json-rpc/README.md. (default 1)
      --backend string                   Backend selection (see 'dlv help backend'). (default "default")
      --build-flags string               Build flags, to be passed to the compiler.
      --check-go-version                 Checks that the version of Go in use is compatible with Delve. (default true)
      --headless                         Run debug server only, in headless mode.
      --init string                      Init file, executed by the terminal client.
  -l, --listen string                    Debugging server listen address. (default "127.0.0.1:0")
      --log                              Enable debugging server logging.
      --log-dest string                  Writes logs to the specified file or file descriptor (see 'dlv help log').
      --log-output string                Comma separated list of components that should produce debug output (see 'dlv help log')
      --only-same-user                   Only connections from the same user that started this instance of Delve are allowed to connect. (default true)
  -r, --redirect stringArray             Specifies redirect rules for target process (see 'dlv help redirect')
      --wd string                        Working directory for running the program.
```

### SEE ALSO
* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

