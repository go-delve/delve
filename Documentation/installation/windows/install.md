# Installation on Windows

Please use the standard `go get` command to build and install Delve on Windows.

```
go get github.com/go-delve/delve/cmd/dlv
```

Note: if you are using Go in modules mode you must execute this command outside of a module directory or Delve will be added to your project as a dependency.

Also, if not already set, you have to add the %GOPATH%\bin directory to your PATH variable.
