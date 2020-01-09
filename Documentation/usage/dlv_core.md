## dlv core

Examine a core dump.

### Synopsis


Examine a core dump.

The core command will open the specified core file and the associated
executable and let you examine the state of the process when the
core dump was taken.

Currently supports linux/amd64 core files and windows/amd64 minidumps.

```
dlv core <executable> <core>
```

### Options inherited from parent commands

```
      --accept-multiclient   Allows a headless server to accept multiple client connections.
      --api-version int      Selects API version when headless. (default 1)
      --backend string       Backend selection (see 'dlv help backend'). (default "default")
      --build-flags string   Build flags, to be passed to the compiler.
      --check-go-version     Checks that the version of Go in use is compatible with Delve. (default true)
      --headless             Run debug server only, in headless mode.
      --init string          Init file, executed by the terminal client.
  -l, --listen string        Debugging server listen address. (default "127.0.0.1:0")
      --log                  Enable debugging server logging.
      --log-dest string      Writes logs to the specified file or file descriptor (see 'dlv help log').
      --log-output string    Comma separated list of components that should produce debug output (see 'dlv help log')
      --mtls-ca-crt string   Specify ca crt file path of mtls to establish tcp connection.
      --mtls-crt string      Specify crt file path of mtls to establish tcp connection.
      --mtls-key string      Specify key file path of mtls to establish tcp connection.
      --tls-crt string       Specify crt file path of tls, need to be generated manually by server and set by server/client.
      --tls-key string       Specify key file path of tls, need to be generated manually by server and only set by server.
      --tls-token string     Specify token string for authentication, set same value by server/client.
      --wd string            Working directory for running the program. (default ".")
```

### SEE ALSO
* [dlv](dlv.md)	 - Delve is a debugger for the Go programming language.

