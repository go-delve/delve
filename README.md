![Delve](https://raw.githubusercontent.com/go-delve/delve/master/assets/delve_horizontal.png)

[![license](http://img.shields.io/badge/license-MIT-blue.svg)](https://raw.githubusercontent.com/go-delve/delve/master/LICENSE)
[![GoDoc](https://godoc.org/github.com/go-delve/delve?status.svg)](https://godoc.org/github.com/go-delve/delve)
[![Build Status](https://travis-ci.org/go-delve/delve.svg?branch=master)](https://travis-ci.org/go-delve/delve)
[![Build status](https://ci.appveyor.com/api/projects/status/9e9edx1qlp3145j5/branch/master?svg=true)](https://ci.appveyor.com/project/go-delve/delve)
[![Join the chat at https://gitter.im/go-delve/delve](https://badges.gitter.im/Join%20Chat.svg)](https://gitter.im/go-delve/delve?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge&utm_content=badge)

The Github issue tracker is for **bugs** only. Please use the [developer mailing list](https://groups.google.com/forum/#!forum/delve-dev) for any feature proposals and discussions.

### About Delve

- [Installation](Documentation/installation)
  - [Linux](Documentation/installation/linux/install.md)
  - [macOS](Documentation/installation/osx/install.md)
  - [Windows](Documentation/installation/windows/install.md)
- [Getting Started](Documentation/cli/getting_started.md)
- [Documentation](Documentation)
  - [Command line options](Documentation/usage/dlv.md)
  - [Command line client](Documentation/cli/README.md)
  - [Plugins and GUIs](Documentation/EditorIntegration.md)
- [Contributing](CONTRIBUTING.md)
  - [Internal Documentation](Documentation/internal)
  - [API documentation](Documentation/api)
  - [How to write a Delve client](Documentation/api/ClientHowto.md)

Delve is a debugger for the Go programming language. The goal of the project is to provide a simple, full featured debugging tool for Go. Delve should be easy to invoke and easy to use. Chances are if you're using a debugger, things aren't going your way. With that in mind, Delve should stay out of your way as much as possible.
