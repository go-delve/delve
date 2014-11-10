# Delve

### What is Delve?

Delve is a Go debugger, written in Go.

### Building

Currently, Delve requires the following [patch](https://codereview.appspot.com/117280043/), however this change is vendored until Go 1.4 lands, so the project is go get-able.

For invocation brevity, I prefer this:

```
$ go build -o dlv && mv dlv $GOPATH/bin
```

### Features

* Attach to an already running process
* Launch a process and begin debug session
* Set breakpoints, single step, step over functions, print variable contents

### Usage

The debugger can be launched in three ways:

* Compile, run, and attach in one step:

	```
	$ dlv -run
	```

* Provide the name of the program you want to debug, and the debugger will launch it for you.

	```
	$ dlv -proc path/to/program
	```

* Provide the pid of a currently running process, and the debugger will attach and begin the session.

	```
	$ sudo dlv -pid 44839
	```

### Breakpoints

Delve can insert breakpoints via the `breakpoint` command once inside a debug session, however for ease of debugging, you can also call `runtime.Breakpoint()` and Delve will handle the breakpoint and stop the program at the next source line.

### Commands

Once inside a debugging session, the following commands may be used:

* `break` - Set break point at the entry point of a function, or at a specific file/line. Example: `break foo.go:13`.

* `continue` - Run until breakpoint or program termination.

* `step` - Single step through program.

* `next` - Step over to next source line.

* `threads` - Print the status of all traced goroutines.

* `goroutines` - Print status of all goroutines.

* `print $var` - Evaluate a variable.

### Upcoming features

* In-scope variable setting
* Support for OS X

### License

MIT
