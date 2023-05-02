## Frequently Asked Questions

<!-- BEGIN TOC -->
* [I'm getting an error while compiling Delve / unsupported architectures and OSs](#unsupportedplatforms)
* [How do I use Delve with Docker?](#docker)
* [How can I use Delve to debug a CLI application?](#ttydebug)
* [How can I use Delve for remote debugging?](#remote)
* [Can not set breakpoints or see source listing in a complicated debugging environment](#substpath)
* [Using Delve to debug the Go runtime](#runtime)
<!-- END TOC -->

### <a name="unsupportedplatforms"></a> I'm getting an error while compiling Delve / unsupported architectures and OSs

The most likely cause of this is that you are running an unsupported Operating System or architecture.
Currently Delve supports (GOOS / GOARCH):
* linux / amd64 (86x64)
* linux / arm64 (AARCH64)
* linux / 386
* windows / amd64
* darwin (macOS) / amd64

There is no planned ETA for support of other architectures or operating systems. Bugs tracking requested support are:

- [32bit ARM support](https://github.com/go-delve/delve/issues/328)
- [PowerPC support](https://github.com/go-delve/delve/issues/1564)
- [OpenBSD](https://github.com/go-delve/delve/issues/1477)

See also: [backend test health](backend_test_health.md).

### <a name="docker"></a> How do I use Delve with Docker?

When running the container you should pass the `--security-opt=seccomp:unconfined` option to Docker. You can start a headless instance of Delve inside the container like this:

```
dlv exec --headless --listen :4040 /path/to/executable
```

And then connect to it from outside the container:

```
dlv connect :4040
```

The program will not start executing until you connect to Delve and send the `continue` command.  If you want the program to start immediately you can do that by passing the `--continue` and `--accept-multiclient` options to Delve:

```
dlv exec --headless --continue --listen :4040 --accept-multiclient /path/to/executable
```

Note that the connection to Delve is unauthenticated and will allow arbitrary remote code execution: *do not do this in production*.

### <a name="ttydebug"></a> How can I use Delve to debug a CLI application?

There are three good ways to go about this

1. Run your CLI application in a separate terminal and then attach to it via `dlv attach`. 

1. Run Delve in headless mode via `dlv debug --headless` and then connect to it from
another terminal. This will place the process in the foreground and allow it to access
the terminal TTY.

1. Assign the process its own TTY. This can be done on UNIX systems via the `--tty` flag for the 
`dlv debug` and `dlv exec` commands. For the best experience, you should create your own PTY and 
assign it as the TTY. This can be done via [ptyme](https://github.com/derekparker/ptyme).

### <a name="remote"></a> How can I use Delve for remote debugging?

It is best not to use remote debugging on a public network. If you have to do this, we recommend using ssh tunnels or a vpn connection.  

##### ```Example ``` 

Remote server:
```
dlv exec --headless --listen localhost:4040 /path/to/executable
```

Local client:
1. connect to the server and start a local port forward

```
ssh -NL 4040:localhost:4040 user@remote.ip
```

2. connect local port
```
dlv connect :4040
```

### <a name="substpath"></a> Can not set breakpoints or see source listing in a complicated debugging environment

This problem manifests when one or more of these things happen:

* Can not see source code when the program stops at a breakpoint
* Setting a breakpoint using full path, or through an IDE, does not work

While doing one of the following things:

* **The program is built and run inside a container** and Delve (or an IDE) is remotely connecting to it
* Generally, every time the build environment (VM, container, computer...) differs from the environment where Delve's front-end (dlv or a IDE) runs 
* Using `-trimpath` or `-gcflags=-trimpath`
* Using a build system other than `go build` (eg. bazel)
* Using symlinks in your source tree

If you are affected by this problem then the `list main.main` command (in the command line interface) will have this result:

```
(dlv) list main.main
Showing /path/to/the/mainfile.go:42 (PC: 0x47dfca)
Command failed: open /path/to/the/mainfile.go: no such file or directory
(dlv)
```

This is not a bug. The Go compiler embeds the paths of source files into the executable so that debuggers, including Delve, can use them. Doing any of the things listed above will prevent this feature from working seamlessly.

The substitute-path feature can be used to solve this problem, see `help config` or the `substitutePath` option in launch.json.

The `sources` command could also be useful in troubleshooting this problem, it shows the list of file paths that has been embedded by the compiler into the executable.

For more informations on path substitution see [path substitution](cli/substitutepath.md).

If you still think this is a bug in Delve and not a configuration problem, open an [issue](https://github.com/go-delve/delve/issues), filling the issue template and including the logs produced by delve with the options `--log --log-output=rpc,dap`.

### <a name="runtime"></a> Using Delve to debug the Go runtime

It's possible to use Delve to debug the Go runtime, however there are some caveats to keep in mind

* The `runtime` package is always compiled with optimizations and inlining, all of the caveats that apply to debugging optimized binaries apply to the runtime package. In particular some variables could be unavailable or have stale values and it could expose some bugs with the compiler assigning line numbers to instructions.

* Next, step and stepout try to follow the current goroutine, if you debug one of the functions in the runtime that modify the curg pointer they will get confused. The 'step-instruction' command should be used instead.
 
* When executing a stacktrace from g0 Delve will return the top frame and then immediately switch to the goroutine stack. If you want to see the g0 stacktrace use `stack -mode simple`.

* The step command only steps into private runtime functions if it is already inside a runtime function. To step inside a private runtime function inserted into user code by the compiler set a breakpoint and then use `runtime.curg.goid == <current goroutine id>` as condition.
