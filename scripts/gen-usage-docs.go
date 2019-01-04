// +build ignore

package main

import (
	"github.com/go-delve/delve/cmd/dlv/cmds"
	"github.com/spf13/cobra/doc"
)

func main() {
	doc.GenMarkdownTree(cmds.New(true), "./Documentation/usage")
}
