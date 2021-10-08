## dlv dap

[EXPERIMENTAL] Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).

### Synopsis

[EXPERIMENTAL] Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).

The server is always headless and requires a DAP client like vscode to connect and request a binary
to be launched or process to be attached to. The following modes can be specified via client's launch config:
- launch + exec (executes precompiled binary, like 'dlv exec')
- launch + debug (builds and launches, like 'dlv debug')
- launch + test (builds and tests, like 'dlv test')
- launch + replay (replays an rr trace, like 'dlv replay')
- launch + core (replays a core dump file, like 'dlv core')
- attach + local (attaches to a running process, like 'dlv attach')
Program and output binary paths will be interpreted relative to dlv's working directory.

The server does not yet accept multiple client connections (--accept-multiclient).
While --continue is not supported, stopOnEntry launch/attach attribute can be used to control if
execution is resumed at the start of the debug session.

The --client-addr flag is a special flag that makes the server initiate a debug session
by dialing in to the host:port where a DAP client is waiting. This server process
will exit when the debug session ends.

```
dlv dap [flags]
```

### Options

```
      --client-addr string   host:port where the DAP client is waiting for the DAP server to dial in
  -h, --help                 help for dap
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

