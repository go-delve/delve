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
      --accept-multiclient   Allows a headless server to accept multiple client connections. Note that the server API is not reentrant and clients will have to coordinate.
      --api-version int      Selects API version when headless. (default 1)
      --backend string       Backend selection:
	default		Uses lldb on macOS, native everywhere else.
	native		Native backend.
	lldb		Uses lldb-server or debugserver.
	rr		Uses mozilla rr (https://github.com/mozilla/rr).
 (default "default")
      --build-flags string   Build flags, to be passed to the compiler.
      --check-go-version     Checks that the version of Go in use is compatible with Delve. (default true)
      --headless             Run debug server only, in headless mode.
      --init string          Init file, executed by the terminal client.
  -l, --listen string        Debugging server listen address. (default "127.0.0.1:0")
      --log                  Enable debugging server logging.
      --log-dest string      Writes logs to the specified file or file descriptor. If the argument is a number it will be interpreted as a file descriptor, otherwise as a file path. This option will also redirect the "API listening" message in headless mode.
      --log-output string    Comma separated list of components that should produce debug output, possible values:
	debugger	Log debugger commands
	gdbwire		Log connection to gdbserial backend
	lldbout		Copy output from debugserver/lldb to standard output
	debuglineerr	Log recoverable errors reading .debug_line
	rpc		Log all RPC messages
	fncall		Log function call protocol
	minidump	Log minidump loading
Defaults to "debugger" when logging is enabled with --log.
      --wd string            Working directory for running the program. (default ".")
```

### SEE ALSO
* [dlv attach](dlv_attach.md)	 - Attach to running process and begin debugging.
* [dlv connect](dlv_connect.md)	 - Connect to a headless debug server.
* [dlv core](dlv_core.md)	 - Examine a core dump.
* [dlv debug](dlv_debug.md)	 - Compile and begin debugging main package in current directory, or the package specified.
* [dlv exec](dlv_exec.md)	 - Execute a precompiled binary, and begin a debug session.
* [dlv replay](dlv_replay.md)	 - Replays a rr trace.
* [dlv run](dlv_run.md)	 - Deprecated command. Use 'debug' instead.
* [dlv test](dlv_test.md)	 - Compile test binary and begin debugging program.
* [dlv trace](dlv_trace.md)	 - Compile and begin tracing program.
* [dlv version](dlv_version.md)	 - Prints version.

