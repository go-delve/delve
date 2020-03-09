package terminal

import (
	"fmt"
	"io"
	"strings"
)

func replaceDocPath(s string) string {
	const docpath = "$GOPATH/src/github.com/go-delve/delve/"

	for {
		start := strings.Index(s, docpath)
		if start < 0 {
			return s
		}
		var end int
		for end = start + len(docpath); end < len(s); end++ {
			if s[end] == ' ' {
				break
			}
		}

		text := s[start+len(docpath) : end]
		s = s[:start] + fmt.Sprintf("[%s](//github.com/go-delve/delve/tree/master/%s)", text, text) + s[end:]
	}
}

func (commands *Commands) WriteMarkdown(w io.Writer) {
	fmt.Fprint(w, "# Configuration and Command History\n\n")
	fmt.Fprint(w, "If `$XDG_CONFIG_HOME` is set, then configuration and command history files are located in `$XDG_CONFIG_HOME/dlv`. ")
	fmt.Fprint(w, "Otherwise, they are located in `$HOME/.config/dlv` on Linux and `$HOME/.dlv` on other systems.\n\n")
	fmt.Fprint(w, "The configuration file `config.yml` contains all the configurable options and their default values. ")
	fmt.Fprint(w, "The command history is stored in `.dbg_history`.\n\n")

	fmt.Fprint(w, "# Commands\n")

	for _, cgd := range commandGroupDescriptions {
		fmt.Fprintf(w, "\n## %s\n\n", cgd.description)

		fmt.Fprint(w, "Command | Description\n")
		fmt.Fprint(w, "--------|------------\n")
		for _, cmd := range commands.cmds {
			if cmd.group != cgd.group {
				continue
			}
			h := cmd.helpMsg
			if idx := strings.Index(h, "\n"); idx >= 0 {
				h = h[:idx]
			}
			fmt.Fprintf(w, "[%s](#%s) | %s\n", cmd.aliases[0], cmd.aliases[0], h)
		}
		fmt.Fprint(w, "\n")

	}

	for _, cmd := range commands.cmds {
		fmt.Fprintf(w, "## %s\n%s\n\n", cmd.aliases[0], replaceDocPath(cmd.helpMsg))
		if len(cmd.aliases) > 1 {
			fmt.Fprint(w, "Aliases:")
			for _, alias := range cmd.aliases[1:] {
				fmt.Fprintf(w, " %s", alias)
			}
			fmt.Fprint(w, "\n")
		}
		fmt.Fprint(w, "\n")
	}
}
