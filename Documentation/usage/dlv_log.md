## dlv log

Help about logging flags.

### Synopsis

Logging can be enabled by specifying the --log flag and using the
--log-output flag to select which components should produce logs.

The argument of --log-output must be a comma separated list of component
names selected from this list:


	debugger	Log debugger commands
	gdbwire		Log connection to gdbserial backend
	lldbout		Copy output from debugserver/lldb to standard output
	debuglineerr	Log recoverable errors reading .debug_line
	rpc		Log all RPC messages
	dap		Log all DAP messages
	fncall		Log function call protocol
	minidump	Log minidump loading

Additionally --log-dest can be used to specify where the logs should be
written. 
If the argument is a number it will be interpreted as a file descriptor,
otherwise as a file path.
This option will also redirect the "server listening at" message in headless
and dap modes.



### Options

```
  -h, --help   help for log
```

### Options inherited from parent commands

```
      --accept-multiclient               Allows a headless server to accept multiple client connections.
      --allow-non-terminal-interactive   Allows interactive sessions of Delve that don't have a terminal as stdin, stdout and stderr
      --api-version int                  Selects API version when headless. New clients should use v2. Can be reset via RPCServer.SetApiVersion. See Documentation/api/json-rpc/README.md. (default 1)
      --backend string                   Backend selection (see 'dlv help backend'). (default "default")
      --build-flags string               Build flags, to be passed to the compiler. For example: --build-flags="-tags=integration -mod=vendor -cover -v"
      --check-go-version                 Exits if the version of Go in use is not compatible (too old or too new) with the version of Delve. (default true)
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

* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

