//go:build ignore

package main

import (
	"bufio"
	"log"
	"os"

	"github.com/go-delve/delve/pkg/terminal"
)

func main() {
	fh, err := os.Create("./Documentation/cli/README.md")
	if err != nil {
		log.Fatalf("could not create README.md: %v", err)
	}
	defer fh.Close()

	w := bufio.NewWriter(fh)
	defer w.Flush()

	commands := terminal.DebugCommands(nil)
	commands.WriteMarkdown(w)
}
