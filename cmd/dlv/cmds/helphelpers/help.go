package helphelpers

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Prepare prepares cmd flag set for the invocation of its usage function by
// hiding flags that we want cobra to parse but we don't want to show to the
// user.
// We do this because not all flags associated with the root command are
// valid for all subcommands but we don't want to move them out of the root
// command and into subcommands, since that would change how cobra parses
// the command line.
//
// For example:
//
//	dlv --headless debug
//
// must parse successfully even though the headless flag is not applicable
// to the 'connect' subcommand.
//
// Prepare is a destructive command, cmd can not be reused after it has been
// called.
func Prepare(cmd *cobra.Command) {
	switch cmd.Name() {
	case "dlv", "help", "run", "version":
		hideAllFlags(cmd)
	case "attach":
		hideFlag(cmd, "build-flags")
		hideFlag(cmd, "disable-aslr")
		hideFlag(cmd, "redirect")
		hideFlag(cmd, "wd")
	case "connect":
		hideFlag(cmd, "accept-multiclient")
		hideFlag(cmd, "allow-non-terminal-interactive")
		hideFlag(cmd, "api-version")
		hideFlag(cmd, "build-flags")
		hideFlag(cmd, "check-go-version")
		hideFlag(cmd, "disable-aslr")
		hideFlag(cmd, "headless")
		hideFlag(cmd, "listen")
		hideFlag(cmd, "only-same-user")
		hideFlag(cmd, "redirect")
		hideFlag(cmd, "wd")
	case "dap":
		hideFlag(cmd, "headless")
		hideFlag(cmd, "accept-multiclient")
		hideFlag(cmd, "init")
		hideFlag(cmd, "backend")
		hideFlag(cmd, "build-flags")
		hideFlag(cmd, "wd")
		hideFlag(cmd, "redirect")
		hideFlag(cmd, "api-version")
		hideFlag(cmd, "allow-non-terminal-interactive")
	case "debug", "test":
		// All flags apply
	case "exec":
		hideFlag(cmd, "build-flags")
	case "replay", "core":
		hideFlag(cmd, "backend")
		hideFlag(cmd, "build-flags")
		hideFlag(cmd, "disable-aslr")
		hideFlag(cmd, "redirect")
		hideFlag(cmd, "wd")
	case "trace":
		hideFlag(cmd, "accept-multiclient")
		hideFlag(cmd, "allow-non-terminal-interactive")
		hideFlag(cmd, "api-version")
		hideFlag(cmd, "headless")
		hideFlag(cmd, "init")
		hideFlag(cmd, "listen")
		hideFlag(cmd, "only-same-user")
	}
}

func hideAllFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		flag.Hidden = true
	})
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		flag.Hidden = true
	})
}

func hideFlag(cmd *cobra.Command, name string) {
	if cmd == nil {
		return
	}
	flag := cmd.Flags().Lookup(name)
	if flag != nil {
		flag.Hidden = true
		return
	}
	hideFlag(cmd.Parent(), name)
}
