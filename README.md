![Delve](https://raw.githubusercontent.com/derekparker/delve/master/assets/delve_horizontal.png)

[![GoDoc](https://godoc.org/github.com/derekparker/delve?status.svg)](https://godoc.org/github.com/derekparker/delve)

### What is Delve?

Delve is a Go debugger, written in Go.

**This project is currently pre 1.0. Most of the functionality is there, however there are various improvements to be made. Delve is not _yet_ ready for daily use.**

### Building

Delve requires Go 1.4 or greater to build.

```
go get -u github.com/derekparker/delve/cmd/dlv
```

#### Linux

You're done!

#### OS X

If you are on OS X a few extra steps must be taken. You must create a self signed certificate and sign the binary with it:

* Open application “Keychain Access” (/Applications/Utilities/Keychain Access.app)
* Open menu /Keychain Access/Certificate Assistant/Create a Certificate...
* Choose a name (dlv-cert in the example), set “Identity Type” to “Self Signed Root”, set “Certificate Type” to “Code Signing” and select the “Let me override defaults”. Click “Continue”. You might want to extend the predefined 365 days period to 3650 days.
* Click several times on “Continue” until you get to the “Specify a Location For The Certificate” screen, then set “Keychain to System”.
* If you can't store the certificate in the “System” keychain, create it in the “login” keychain, then export it. You can then import it into the “System” keychain.
* In keychains select “System”, and you should find your new certificate. Use the context menu for the certificate, select “Get Info”, open the “Trust” item, and set “Code Signing” to “Always Trust”.
* You must quit “Keychain Access” application in order to use the certificate and restart “taskgated” service by killing the current running “taskgated” process. Alternatively you can restart your computer.
* Run the following: `CERT=$mycert make install`, which will install the binary and codesign it.

All `make` commands assume a CERT environment variables that contains the name of the cert you created above.
Following that you can `CERT=mycert make install` which should install the binary and codesign it. For running tests, simply run `CERT=mycert make test`.

The makefile is only necessary to help facilitate the process of building and codesigning.

See [Tips and troubleshooting](#tips-and-troubleshooting) for additional OS X setup information.

### Usage

The debugger can be launched in three ways:

* Compile, run, and attach in one step:

	```
	$ dlv run
	```

* Compile test binary, run and attach:

	```
	$ dlv test
	```

* Provide the name of the program you want to debug, and the debugger will launch it for you.

	```
	$ dlv path/to/program
	```

* Provide the pid of a currently running process, and the debugger will attach and begin the session.

	```
	$ sudo dlv attach 44839
	```

### Breakpoints

Delve can insert breakpoints via the `breakpoint` command once inside a debug session, however for ease of debugging, you can also use the `runtime.Breakpoint()` function in your source code and Delve will handle the breakpoint and stop the program at the next source line.

### Commands

Once inside a debugging session, the following commands may be used:

* `help` - Prints the help message.

* `break` - Set a breakpoint. Example: `break foo.go:13` or `break main.main`.

* `continue` - Run until breakpoint or program termination.

* `step` - Single step through program.

* `next` - Step over to next source line.

* `threads` - Print status of all traced threads.

* `thread $tid` - Switch to another thread.

* `goroutines` - Print status of all goroutines.

* `breakpoints` - Print information on all active breakpoints.

* `print $var` - Evaluate a variable.

* `info $type [regex]` - Outputs information about the symbol table. An optional regex filters the list. Example `info funcs unicode`. Valid types are:
  * `args` - Prints the name and value of all arguments to the current function
  * `funcs` - Prints the name of all defined functions
  * `locals` - Prints the name and value of all local variables in the current context
  * `sources` - Prints the path of all source files
  * `vars` - Prints the name and value of all package variables in the app. Any variable that is not local or arg is considered a package variables

* `exit` - Exit the debugger.

### Tips and troubleshooting

#### OS X

##### Eliminating codesign authorization prompt during builds

If you're prompted for authorization when running `make` using your self-signed certificate, try the following:

* Open application “Keychain Access” (/Applications/Utilities/Keychain Access.app)
* Double-click on the private key corresponding to your self-signed certificate (dlv-cert in the example)
* Choose the "Access Control" tab
* Click the [+] under "Always allow access by these applications", and choose `/usr/bin/codesign` from the Finder dialog
* Click the "Save changes" button

##### Eliminating "Developer tools access" prompt running delve

If you are prompted with this when running `dlv`:

    "Developer tools access needs to take control of another process for debugging to continue. Type your password to allow this"

Try running `DevToolsSecurity -enable` to eliminate the prompt. See `man DevToolsSecurity` for more information.


### Upcoming features

* In-scope variable setting
* Editor integration

### License

MIT
