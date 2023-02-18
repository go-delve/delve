![Delve](https://raw.githubusercontent.com/go-delve/delve/master/assets/delve_horizontal.png)

[![license](http://img.shields.io/badge/license-MIT-blue.svg)](https://raw.githubusercontent.com/go-delve/delve/master/LICENSE)
[![GoDoc](https://godoc.org/github.com/go-delve/delve?status.svg)](https://godoc.org/github.com/go-delve/delve)
[![Build Status](https://delve.teamcity.com/app/rest/builds/buildType:(id:Delve_AggregatorBuild)/statusIcon.svg)](https://delve.teamcity.com/viewType.html?buildTypeId=Delve_AggregatorBuild&guest=1)

The GitHub issue tracker is for **bugs** only. Please use the [developer mailing list](https://groups.google.com/forum/#!forum/delve-dev) for any feature proposals and discussions.

### About Delve

- [Installation](Documentation/installation)
- [Getting Started](Documentation/cli/getting_started.md)
- [Documentation](Documentation)
  - [Command line options](Documentation/usage/dlv.md)
  - [Command line client](Documentation/cli/README.md)
  - [Plugins and GUIs](Documentation/EditorIntegration.md)
  - [Frequently Asked Questions](Documentation/faq.md)
- [Contributing](CONTRIBUTING.md)
  - [Internal Documentation](Documentation/internal)
  - [API documentation](Documentation/api)
  - [How to write a Delve client](Documentation/api/ClientHowto.md)

Delve is a debugger for the Go programming language. The goal of the project is to provide a simple, full featured debugging tool for Go. Delve should be easy to invoke and easy to use. Chances are if you're using a debugger, things aren't going your way. With that in mind, Delve should stay out of your way as much as possible.
