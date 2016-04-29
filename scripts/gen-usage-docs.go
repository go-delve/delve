package main

import (
	"github.com/derekparker/delve/cmd/dlv/cmds"
	"github.com/spf13/cobra/doc"
)

func main() {
	doc.GenMarkdownTree(cmds.New(), "./Documentation/usage")
}
