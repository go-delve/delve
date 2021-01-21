// Generates .travis.yml configuration using pkg/goversion/compat.go
// Usage go run scripts/gen-travis.go > .travis.yml

package main

import (
	"bufio"
	"fmt"
	"os"
	"text/template"

	"github.com/go-delve/delve/pkg/goversion"
)

type arguments struct {
	GoVersions []goVersion
}

type goVersion struct {
	Major, Minor int
}

var maxVersion = goVersion{Major: goversion.MaxSupportedVersionOfGoMajor, Minor: goversion.MaxSupportedVersionOfGoMinor}
var minVersion = goVersion{Major: goversion.MinSupportedVersionOfGoMajor, Minor: goversion.MinSupportedVersionOfGoMinor}

func (v goVersion) dec() goVersion {
	v.Minor--
	if v.Minor < 0 {
		panic("TODO: fill the maximum minor version number for v.Maxjor here")
	}
	return v
}

func (v goVersion) MaxVersion() bool {
	return v == maxVersion
}

func (v goVersion) DotX() string {
	return fmt.Sprintf("%d.%d.x", v.Major, v.Minor)
}

func (v goVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func main() {
	var args arguments

	args.GoVersions = append(args.GoVersions, maxVersion)
	for {
		v := args.GoVersions[len(args.GoVersions)-1].dec()
		args.GoVersions = append(args.GoVersions, v)
		if v == minVersion {
			break
		}
	}

	githubfh, err := os.Create(".github/workflows/test.yml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not create .github/test.yml: %v", err)
		os.Exit(1)
	}
	out := bufio.NewWriter(githubfh)
	err = template.Must(template.New(".github/workflows/test.yml").Parse(`name: Delve CI

on: [push, pull_request]

jobs:
  build:
    runs-on: ${{"{{"}}matrix.os{{"}}"}}
    strategy:
      matrix:
        include:
          - go: {{index .GoVersions 0}}
            os: macos-latest
          - go: {{index .GoVersions 1}}
            os: ubuntu-latest
          - go: {{index .GoVersions 2}}
            os: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v1
        with:
          go-version: ${{"{{"}}matrix.go{{"}}"}}
      - run: go run _scripts/make.go test
`)).Execute(out, args)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing template: %v", err)
		os.Exit(1)
	}

	_ = out.Flush()
	_ = githubfh.Close()
}
