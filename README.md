# Delve

[![GoDoc](https://godoc.org/github.com/derekparker/delve?status.svg)](https://godoc.org/github.com/derekparker/delve)

### What is Delve?

Delve is a (Beta) Go debugger, written in Go.

This project is currently in beta. Most of the functionality is there, but there are various improvements to be made.

### Building

Delve requires Go 1.4 to build.

```
go get -u github.com/derekparker/delve/cmd/dlv
```

#### Linux

You're done!

#### OS X

If you are on OS X a few extra steps must be taken. You must create a self signed certificate:

* Open application “Keychain Access” (/Applications/Utilities/Keychain Access.app)
* Open menu /Keychain Access/Certificate Assistant/Create a Certificate...
* Choose a name (dlv-cert in the example), set “Identity Type” to “Self Signed Root”, set “Certificate Type” to “Code Signing” and select the “Let me override defaults”. Click “Continue”. You might want to extend the predefined 365 days period to 3650 days.
* Click several times on “Continue” until you get to the “Specify a Location For The Certificate” screen, then set “Keychain to System”.
* If you can't store the certificate in the “System” keychain, create it in the “login” keychain, then export it. You can then import it into the “System” keychain.
* In keychains select “System”, and you should find your new certificate. Use the context menu for the certificate, select “Get Info”, open the “Trust” item, and set “Code Signing” to “Always Trust”.
* You must quit “Keychain Access” application in order to use the certificate and restart “taskgated” service by killing the current running “taskgated” process. Alternatively you can restart your computer.

All `make` commands assume a CERT environment variables that contains the name of the cert you created above.
Following that you can `CERT=mycert make install` which should install the binary and codesign it. For running tests, simply run `CERT=mycert make test`.

The makefile is only necessary to help facilitate the process of building and codesigning.

### Features

* Attach to an already running process
* Launch a process and begin debug session
* Set breakpoints, single step, step over functions, print variable contents, print thread and goroutine information

### Usage

The debugger can be launched in three ways:

* Compile, run, and attach in one step:

	```
	$ dlv -run
	```

* Provide the name of the program you want to debug, and the debugger will launch it for you.

	```
	$ dlv path/to/program
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

* `threads` - Print status of all traced threads.

* `goroutines` - Print status of all goroutines.

* `print $var` - Evaluate a variable.

* `info $type [regex]` - Outputs information about the symbol table. An optional regex filters the list. Example `info funcs unicode`. Valid types are:
  * `args` - Prints the name and value of all arguments to the current function
  * `funcs` - Prings the name of all defined functions
  * `locals` - Prints the name and value of all local variables in the current context
  * `sources` - Prings the path of all source files
  * `vars` - Prints the name and value of all package variables in the app. Any variable that is not local or arg is considered a package variables

* `exit` - Exit the debugger.


### Upcoming features

* In-scope variable setting
* Editor integration

### License

MIT
