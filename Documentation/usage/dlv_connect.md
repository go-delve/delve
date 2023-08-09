## dlv connect

Connect to a headless debug server with a terminal client.

### Synopsis

Connect to a running headless debug server with a terminal client.

```
dlv connect addr [flags]
```

### Options

```
  -h, --help   help for connect
```

### Options inherited from parent commands

```
      --backend string      Backend selection (see 'dlv help backend'). (default "default")
      --init string         Init file, executed by the terminal client.
      --log                 Enable debugging server logging.
      --log-dest string     Writes logs to the specified file or file descriptor (see 'dlv help log').
      --log-output string   Comma separated list of components that should produce debug output (see 'dlv help log')
```

### SEE ALSO

* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

