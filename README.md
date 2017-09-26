![Delve](https://raw.githubusercontent.com/derekparker/delve/master/assets/delve_horizontal.png)

[![license](http://img.shields.io/badge/license-MIT-blue.svg)](https://raw.githubusercontent.com/derekparker/delve/master/LICENSE)
[![GoDoc](https://godoc.org/github.com/derekparker/delve?status.svg)](https://godoc.org/github.com/derekparker/delve)
[![Build Status](https://travis-ci.org/derekparker/delve.svg?branch=travis-ci)](https://travis-ci.org/derekparker/delve)
[![Build status](https://ci.appveyor.com/api/projects/status/9e9edx1qlp3145j5?svg=true)](https://ci.appveyor.com/project/derekparker/delve)
[![Join the chat at https://gitter.im/derekparker/delve](https://badges.gitter.im/Join%20Chat.svg)](https://gitter.im/derekparker/delve?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge&utm_content=badge)

The Github issue tracker is for **bugs** only. Please use the [developer mailing list](https://groups.google.com/forum/#!forum/delve-dev) for any feature proposals and discussions.

### About Delve

- [Documentation](Documentation)
- [Building](Documentation/installation)
- [Contributing](CONTRIBUTING.md)

Delve is a debugger for the Go programming language. The goal of the project is to provide a simple, full featured debugging tool for Go. Delve should be easy to invoke and easy to use. Chances are if you're using a debugger, things aren't going your way. With that in mind, Delve should stay out of your way as much as possible.

### Quickstart

The example sequence of commands shows you how to set a breakpoint on `main.main`, continue to the breakpoint, list the source code at the breakpoint, and then proceed through the program one line at a time.

```
$ dlv exec ./hello -- server --config conf/config.toml
(dlv) break main.main
(dlv) c
(dlv) list
(dlv) next
```

Use `step` to "step in" to a function.

For more details, see the [Documentation](Documentation).
