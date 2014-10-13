# DBG

### What is DBG?

DBG is a Go debugger, written primarily in Go. It really needs a new name.

### Building

Currently, DBG requires the following [patch](https://codereview.appspot.com/117280043/), however this change is vendored until Go 1.4 lands.

### Features

* Attach to (trace) a running process
* Ability to launch a process and begin debugging it
* Set breakpoints
* Single step through a process
* Next through a process (step over / out of subroutines)
* Never retype commands, empty line defaults to previous command
* Readline integration

### Usage

The debugger can be launched in three ways:

* Allow it to compile, run, and attach to a program:

	```
	$ dbg -run
	```

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

* Handle Gos multithreaded nature better
* In-scope variable evaluation
* In-scope variable setting
* Support for OS X

### License

MIT
