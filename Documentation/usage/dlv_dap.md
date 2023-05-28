## dlv dap

Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).

### Synopsis

Starts a headless TCP server communicating via Debug Adaptor Protocol (DAP).

The server is always headless and requires a DAP client like VS Code to connect and request a binary
to be launched or a process to be attached to. The following modes can be specified via the client's launch config:
- launch + exec   (executes precompiled binary, like 'dlv exec')
- launch + debug  (builds and launches, like 'dlv debug')
- launch + test   (builds and tests, like 'dlv test')
- launch + replay (replays an rr trace, like 'dlv replay')
- launch + core   (replays a core dump file, like 'dlv core')
- attach + local  (attaches to a running process, like 'dlv attach')

Program and output binary paths will be interpreted relative to dlv's working directory.

This server does not accept multiple client connections (--accept-multiclient).
Use 'dlv [command] --headless' instead and a DAP client with attach + remote config.
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
      --check-go-version    Exits if the version of Go in use is not compatible (too old or too new) with the version of Delve. (default true)
      --disable-aslr        Disables address space randomization
  -l, --listen string       Debugging server listen address. (default "127.0.0.1:0")
      --log                 Enable debugging server logging.
      --log-dest string     Writes logs to the specified file or file descriptor (see 'dlv help log').
      --log-output string   Comma separated list of components that should produce debug output (see 'dlv help log')
      --only-same-user      Only connections from the same user that started this instance of Delve are allowed to connect. (default true)
```

### SEE ALSO

* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

