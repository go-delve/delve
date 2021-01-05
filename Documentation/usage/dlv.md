## dlv

Delve is a debugger for the Go programming language.

### Synopsis


Delve is a source level debugger for Go programs.

Delve enables you to interact with your program by controlling the execution of the process,
evaluating variables, and providing information of thread / goroutine state, CPU register state and more.

The goal of this tool is to provide a simple yet powerful interface for debugging Go programs.

Pass flags to the program you are debugging using `--`, for example:

`dlv exec ./hello -- server --config conf/config.toml`

### Options

```
      --accept-multiclient               Allows a headless server to accept multiple client connections.
      --allow-non-terminal-interactive   Allows interactive sessions of Delve that don't have a terminal as stdin, stdout and stderr
      --api-version int                  Selects API version when headless. New clients should use v2. Can be reset via RPCServer.SetApiVersion. See Documentation/api/json-rpc/README.md. (default 1)
      --backend string                   Backend selection (see 'dlv help backend'). (default "default")
      --build-flags string               Build flags, to be passed to the compiler. For example: --build-flags="-tags=integration -mod=vendor -cover -v"
      --check-go-version                 Checks that the version of Go in use is compatible with Delve. (default true)
      --disable-aslr                     Disables address space randomization
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
* [dlv attach](dlv_attach.md)	 - Attach to running process and begin debugging.
* [dlv connect](dlv_connect.md)	 - Connect to a headless debug server.
* [dlv core](dlv_core.md)	 - Examine a core dump.
* [dlv dap](dlv_dap.md)	 - [EXPERIMENTAL] Starts a TCP server communicating via Debug Adaptor Protocol (DAP).
* [dlv debug](dlv_debug.md)	 - Compile and begin debugging main package in current directory, or the package specified.
* [dlv exec](dlv_exec.md)	 - Execute a precompiled binary, and begin a debug session.
* [dlv replay](dlv_replay.md)	 - Replays a rr trace.
* [dlv run](dlv_run.md)	 - Deprecated command. Use 'debug' instead.
* [dlv test](dlv_test.md)	 - Compile test binary and begin debugging program.
* [dlv trace](dlv_trace.md)	 - Compile and begin tracing program.
* [dlv version](dlv_version.md)	 - Prints version.

* [dlv log](dlv_log.md)	 - Help about logging flags
* [dlv backend](dlv_backend.md)	 - Help about the `--backend` flag
