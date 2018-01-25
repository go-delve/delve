# Getting Started

Delve aims to be a very simple and powerful tool, but can be confusing if you're
not used to using a source level debugger in a compiled language. This document
will provide all the information you need to get started debugging your Go
programs.

This guide was developed with Delve `Version: 1.0.0-rc.2`, built with `go version go1.9.2 linux/amd64`
in an Ubuntu 16.04 (`Linux 4.8.0-36-generic x86_64`).


## Debugging 'main' packages

The first CLI subcommand we will explore is `debug`. This subcommand can be run
without arguments if you're in the same directory as your `main` package,
otherwise it optionally accepts a package path.

For example given the hello world example from this guide:

```
$GOPATH
├── src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world
├── cmd
│   └── hello_world
│       └────────── main.go
```

If you are in the directory `src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world` you can simple run `dlv debug`
from the command line. From anywhere else, say the project root, you can simply
provide the package: `dlv debug github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world`.

Invoking that command will cause Delve to compile the program in a way most
suitable for debugging, then it will execute and attach to the program and begin
a debug session. Now, when the debug session has first started you are at the
very beginning of the program's initialization, not at your program's `main` function. To get to someplace more useful
you're going to want to set a breakpoint or two and continue execution to that
point.

For example, to continue execution to your program's `main` function:

```
$ dlv debug github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world
Type 'help' for list of commands.
(dlv) break main.main
Breakpoint 1 set at 0x49ecf3 for main.main() ./go/src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world/main.go:6
(dlv) continue
> main.main() ./go/src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world/main.go:6 (hits goroutine(1):1 total:1) (PC: 0x49ecf3)
Warning: debugging optimized function
     1:   package main
     2:   
     3:   
     4:   import "fmt"
     5:   
=>   6:   func main() {
     7:        fmt.Println("hello world")
     8:   }
(dlv) 
```

To step over one line of source code at a time and execute the `Println` statement the command [`next`](https://github.com/derekparker/delve/tree/master/Documentation/cli#next) is used:

```
(dlv) next
> main.main() ./go/src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world/main.go:7 (PC: 0x49ed01)
Warning: debugging optimized function
     2:   
     3:   
     4:   import "fmt"
     5:   
     6:   func main() {
=>   7:        fmt.Println("hello world")
     8:   }
(dlv) next
hello world
> main.main() ./go/src/github.com/derekparker/delve/Documentation/getting_started/examples/cmd/hello_world/main.go:8 (PC: 0x49ed72)
Warning: debugging optimized function
     3:   
     4:   import "fmt"
     5:   
     6:   func main() {
     7:        fmt.Println("hello world")
=>   8:   }
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
