## dlv trace

Compile and begin tracing program.

### Synopsis

Trace program execution.

The trace sub command will set a tracepoint on every function matching the
provided regular expression and output information when tracepoint is hit.  This
is useful if you do not want to begin an entire debug session, but merely want
to know what functions your process is executing.

The output of the trace sub command is printed to stderr, so if you would like to
only see the output of the trace operations you can redirect stdout.

```
dlv trace [package] regexp [flags]
```

### Options

```
      --ebpf            Trace using eBPF (experimental).
  -e, --exec string     Binary file to exec and trace.
  -h, --help            help for trace
      --output string   Output path for the binary.
  -p, --pid int         Pid to attach to.
  -s, --stack int       Show stack trace with given depth. (Ignored with --ebpf)
  -t, --test            Trace a test binary.
      --timestamp       Show timestamp in the output
```

### Options inherited from parent commands

```
      --backend string         Backend selection (see 'dlv help backend'). (default "default")
      --build-flags string     Build flags, to be passed to the compiler. For example: --build-flags="-tags=integration -mod=vendor -cover -v"
      --check-go-version       Exits if the version of Go in use is not compatible (too old or too new) with the version of Delve. (default true)
      --disable-aslr           Disables address space randomization
      --log                    Enable debugging server logging.
      --log-dest string        Writes logs to the specified file or file descriptor (see 'dlv help log').
      --log-output string      Comma separated list of components that should produce debug output (see 'dlv help log')
  -r, --redirect stringArray   Specifies redirect rules for target process (see 'dlv help redirect')
      --wd string              Working directory for running the program.
```

### SEE ALSO

* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

