[![Build Status](https://github.com/goretk/gore/actions/workflows/go.yml/badge.svg)](https://github.com/goretk/gore/actions/workflows/go.yml)
![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/goretk/gore?label=release&sort=semver)
[![codecov](https://codecov.io/gh/goretk/gore/branch/develop/graph/badge.svg?token=q68t8P9A98)](https://codecov.io/gh/goretk/gore)
[![Go Report Card](https://goreportcard.com/badge/github.com/goretk/gore)](https://goreportcard.com/report/github.com/goretk/gore)
[![Go Reference](https://pkg.go.dev/badge/github.com/goretk/gore.svg)](https://pkg.go.dev/github.com/goretk/gore)
# GoRE - Package gore is a library for analyzing Go binaries

## How to use

1. Use `go get` to download the library.
2. Import it into your project.
3. Write a new cool tool.

For an example use case, please checkout [redress](https://github.com/goretk/redress).

### Sample code

Extract the main package, child packages, and sibling packages:
```go
f, err := gore.Open(fileStr)
pkgs, err := f.GetPackages()
```

Extract all the types in the binary:
```go
f, err := gore.Open(fileStr)
typs, err := f.GetTypes()
```

## Update get new Go release information

Instead of downloading new release of the library to get detection
for new Go releases, it is possible to do a local pull.

Run `go generate` and new compiler releases will be generated from
the git tags.

## Functionality

### Go compiler

The library has functionality for guessing the compiler version
used. It searches the binary for the identifiable string left
by the compiler. It is not perfect, so functionality for assuming
a specific version is also provided. The version strings used are
identical to the identifiers. For example version "1.10.1" is
represented as "go1.10.1" and version "1.10" is represented as
"go1.10"

### Function recovery

Function information is recovered from the pclntab. Information
that is recovered includes: function start and end location in
the text section, source file. The library also tries to estimate
the first and last line number in the source file for the function.
The methods recovered includes the receiver. All functions and
methods belong to a package. The library tries to classify the
type of package. If it is a standard library package, 3rd-party
package, or part of the main application. If it unable to classify
the package, it is classified as unknown.

### Type recovery

The types in the binary are parsed from the "typelink" list. Not
all versions of Go are supported equally. Versions 1.7 and later
are fully supported. Versions 1.5 and 1.6 are partially supported.
Version prior to 1.5 are not supported at all at the moment.

