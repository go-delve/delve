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
  -h, --help   help for dlv
```

### SEE ALSO

* [dlv attach](dlv_attach.md)	 - Attach to running process and begin debugging.
* [dlv connect](dlv_connect.md)	 - Connect to a headless debug server with a terminal client.
* [dlv core](dlv_core.md)	 - Examine a core dump.
* [dlv dap](dlv_dap.md)	 - Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).
* [dlv debug](dlv_debug.md)	 - Compile and begin debugging main package in current directory, or the package specified.
* [dlv exec](dlv_exec.md)	 - Execute a precompiled binary, and begin a debug session.
* [dlv replay](dlv_replay.md)	 - Replays a rr trace.
* [dlv test](dlv_test.md)	 - Compile test binary and begin debugging program.
* [dlv trace](dlv_trace.md)	 - Compile and begin tracing program.
* [dlv version](dlv_version.md)	 - Prints version.

* [dlv log](dlv_log.md)	 - Help about logging flags
* [dlv backend](dlv_backend.md)	 - Help about the `--backend` flag
