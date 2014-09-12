# DBG

### What is DBG?

DBG is a Go debugger, written primarily in Go. It really needs a new name.

### Building

Currently, DBG requires the following [patch](https://codereview.appspot.com/117280043/) to be applied to your Go source to build.

### Features

* Attach to (trace) a running process
* Ability to launch a process and begin debugging it
* Set breakpoints
* Single step through a process
* Next through a process (step over / out of subroutines)
* Never retype commands, empty line defaults to previous command

### Usage

The debugger can be launched in two ways:

* Provide the name of the program you want to debug, and the debugger will launch it for you.
	
	```
	$ dbg -proc path/to/program
	```

* Provide the pid of a currently running process, and the debugger will attach and begin the session.

	```
	$ sudo dbg -pid 44839
	```

Once inside a debugging session, the following commands may be used:

* `break` - Set break point at the entry point of a function, or at a specific file/line. Example: `break foo.go:13`.

* `step` - Single step through program.

* `next` - Step over to next source line.

### Upcoming features

* Handle Gos multithreaded nature better (follow goroutine accross thread contexts)
* In-scope variable evaluation
* In-scope variable setting
* Readline integration
* Support for OS X

### License

MIT
