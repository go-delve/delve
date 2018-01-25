# Getting Started

Delve aims to be a very simple and powerful tool, but can be confusing if you're
not used to using a source level debugger in a compiled language. This document
will provide all the information you need to get started debugging your Go
programs.

## Debugging 'main' packages

The first CLI subcommand we will explore is `debug`. This subcommand can be run
without arguments if you're in the same directory as your `main` package,
otherwise it optionally accepts a package path.

For example given this project layout:

```
.
├── github.com/me/foo
├── cmd
│   └── foo
│       └── main.go
├── pkg
│   └── baz
│       ├── bar.go
│       └── bar_test.go
```

If you are in the directory `github.com/me/foo/cmd/foo` you can simple run `dlv debug`
from the command line. From anywhere else, say the project root, you can simply
provide the package: `dlv debug github.com/me/foo/cmd/foo`. To pass flags to your program 
separate them with `--`: `dlv debug github.com/me/foo/cmd/foo -- -arg1 value`.

Invoking that command will cause Delve to compile the program in a way most
suitable for debugging, then it will execute and attach to the program and begin
a debug session. Now, when the debug session has first started you are at the
very beginning of the program's initialization. To get to someplace more useful
you're going to want to set a breakpoint or two and continue execution to that
point.

For example, to continue execution to your program's `main` function:

```
$ dlv debug github.com/me/foo/cmd/foo
Type 'help' for list of commands.
(dlv) break main.main
Breakpoint 1 set at 0x49ecf3 for main.main() ./test.go:5
(dlv) continue
> main.main() ./test.go:5 (hits goroutine(1):1 total:1) (PC: 0x49ecf3)
     1:	package main
     2:	
     3:	import "fmt"
     4:	
=>   5:	func main() {
     6:		fmt.Println("delve test")
     7:	}
(dlv) 
```

## Debugging tests

Given the same directory structure as above you can debug your code by executing
your test suite. For this you can use the `dlv test` subcommand, which takes the
same optional package path as `dlv debug`, and will also build the current
package if not given any argument.

```
$ dlv test github.com/me/foo/pkg/baz
Type 'help' for list of commands.
(dlv) funcs test.Test*
/home/me/go/src/github.com/me/foo/pkg/baz/test.TestHi
(dlv) break TestHi
Breakpoint 1 set at 0x536513 for /home/me/go/src/github.com/me/foo/pkg/baz/test.TestHi() ./test_test.go:5
(dlv) continue
> /home/me/go/src/github.com/me/foo/pkg/baz/test.TestHi() ./bar_test.go:5 (hits goroutine(5):1 total:1) (PC: 0x536513)
     1:	package baz
     2:	
     3:	import "testing"
     4:	
=>   5:	func TestHi(t *testing.T) {
     6:		t.Fatal("implement me!")
     7:	}
(dlv) 
```

As you can see, we began debugging the test binary, found our test function via
the `funcs` command which takes a regexp to filter the list of functions, set a
breakpoint and then continued execution until we hit that breakpoint.

For more information on subcommands you can use type `dlv help`, and once in a
debug session you can see all of the commands available to you by typing `help`
at any time.
