package config

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/user"
	"path"
	"runtime"
	"slices"

	"github.com/go-delve/delve/service/api"
	"go.yaml.in/yaml/v3"
)

const (
	configDir       string = "dlv"
	configDirHidden string = ".dlv"
	configFile      string = "config.yml"

	PositionSource      = "source"
	PositionDisassembly = "disassembly"
	PositionDefault     = "default"
)

// SubstitutePathRule describes a rule for substitution of path to source code file.
type SubstitutePathRule struct {
	// Directory path will be substituted if it matches `From`.
	From string
	// Path to which substitution is performed.
	To string
}

// SubstitutePathRules is a slice of source code path substitution rules.
type SubstitutePathRules []SubstitutePathRule

// Config defines all configuration options available to be set through the config file.
type Config struct {
	// Commands aliases.
	Aliases map[string][]string `yaml:"aliases"`
	// Source code path substitution rules.
	SubstitutePath SubstitutePathRules `yaml:"substitute-path"`

	// MaxStringLen is the maximum string length that the commands print,
	// locals, args and vars should read (in verbose mode).
	MaxStringLen *int `yaml:"max-string-len,omitempty"`
	// MaxArrayValues is the maximum number of array items that the commands
	// print, locals, args and vars should read (in verbose mode).
	MaxArrayValues *int `yaml:"max-array-values,omitempty"`
	// MaxVariableRecurse is output evaluation depth of nested struct members, array and
	// slice items and dereference pointers
	MaxVariableRecurse *int `yaml:"max-variable-recurse,omitempty"`
	// DisassembleFlavor allow user to specify output syntax flavor of assembly, one of
	// this list "intel"(default), "gnu", "go"
	DisassembleFlavor *string `yaml:"disassemble-flavor,omitempty"`

	// If ShowLocationExpr is true whatis will print the DWARF location
	// expression for its argument.
	ShowLocationExpr bool `yaml:"show-location-expr"`

	// Source list line-number color, as a terminal escape sequence.
	// For historic reasons, this can also be an integer color code.
	SourceListLineColor any `yaml:"source-list-line-color"`

	// Source list arrow color, as a terminal escape sequence.
	SourceListArrowColor string `yaml:"source-list-arrow-color"`

	// Source list keyword color, as a terminal escape sequence.
	SourceListKeywordColor string `yaml:"source-list-keyword-color"`

	// Source list string color, as a terminal escape sequence.
	SourceListStringColor string `yaml:"source-list-string-color"`

	// Source list number color, as a terminal escape sequence.
	SourceListNumberColor string `yaml:"source-list-number-color"`

	// Source list comment color, as a terminal escape sequence.
	SourceListCommentColor string `yaml:"source-list-comment-color"`

	// Color for the prompt line.
	PromptColor string `yaml:"prompt-color"`

	// Source list tab color, as a terminal escape sequence.
	SourceListTabColor string `yaml:"source-list-tab-color"`

	// Color for function names in the stack trace.
	StackTraceFunctionColor string `yaml:"stacktrace-function-color"`

	// Color for the base name in paths in the stack trace.
	StackTraceBasenameColor string `yaml:"stacktrace-basename-color"`

	// number of lines to list above and below cursor when printfile() is
	// called (i.e. when execution stops, listCommand is used, etc)
	SourceListLineCount *int `yaml:"source-list-line-count,omitempty"`

	// DebugInfoDirectories is the list of directories Delve will use
	// in order to resolve external debug info files.
	DebugInfoDirectories []string `yaml:"debug-info-directories"`

	// Position controls how the current position in the program is displayed.
	// There are three possible values:
	//  - source: always show the current position in the program's source
	//    code.
	//  - disassembly: always should the current position by disassembling the
	//    current function.
	//  - default (or the empty string): use disassembly for step-instruction,
	//    source for everything else.
	Position string `yaml:"position"`

	// Tab changes what is printed when a '\t' is encountered in source code.
	// This can be used to shorten the tabstop (e.g. "  ") or to print a more
	// visual indent (e.g. ">__ ").
	Tab string `yaml:"tab"`

	// TraceShowTimestamp controls whether to show timestamp in the trace
	// output.
	TraceShowTimestamp bool `yaml:"trace-show-timestamp"`
}

var Documentation = map[string]string{
	"aliases": `Configures command aliases.
	
	config alias <command> <alias>
	config alias <alias>

Defines <alias> as an alias to <command> or removes an alias.
`,

	"substitute-path": `	config substitute-path <from> <to>
	config substitute-path <from>
	config substitute-path -clear
	config substitute-path -guess

Adds or removes a path substitution rule, if -clear is used all
substitute-path rules are removed. Without arguments shows the current list
of substitute-path rules.
The -guess option causes Delve to try to guess your substitute-path
configuration automatically.
See also Documentation/cli/substitutepath.md for how the rules are applied.
`,

	"max-string-len":            "Maximum string length used when printing variables.\n",
	"max-array-values":          "Maximum number of array values when printing variables.\n",
	"max-variable-recurse":      "Maximum number of nested struct members when printing variables.\n",
	"disassemble-flavor":        "Disassembler syntax. Can be 'intel', 'gun' or 'go'.\n",
	"show-location-expr":        "If true the 'whatis' command will print the DWARF location expression of its argument.\n",
	"source-list-line-color":    "Source list line-number color, as a terminal escape sequence.\n",
	"source-list-arrow-color":   "Source list arrow color, as a terminal escape sequence.\n",
	"source-list-keyword-color": "Source list keyword color, as a terminal escape sequence.\n",
	"source-list-string-color":  "Source list string color, as a terminal escape sequence.\n",
	"source-list-number-color":  "Source list number color, as a terminal escape sequence.\n",
	"source-list-comment-color": "Source list comment color, as a terminal escape sequence.\n",
	"prompt-color":              "Prompt color, as a terminal escape sequence.\n",
	"source-list-tab-color":     "Source list tab color, as a terminal escape sequence.\n",
	"stacktrace-function-color": "Color for function names in the stack trace, as a terminal escape sequence.\n",
	"stacktrace-basename-color": "Color for the base name in paths in the stack trace, as a terminal escape sequence.\n",
	"source-list-line-count":    "Number of lines to list above and below the cursor when printing source code.\n",
	"tab":                       "Changes what is printed when a tab character is encountered in source code.\n",
	"trace-show-timestamp":      "If true timestamps are shown in the trace output.\n",

	"debug-info-directories": `	config debug-info-directories -add <path>
	config debug-info-directories -rm <path>
	config debug-info-directories -clear

Adds, removes or clears debug-info-directories.
`,

	"position": `Controls how the current position in the program is displayed. The possible values are:
 - 'source': always show the current position in the source code.
 - 'disassembly': always show the current position in the program disassembly.
 - 'default': show the current position in the disassembly after 'step-instruction', otherwise in source code.
`,
}

func (c *Config) GetSourceListLineCount() int {
	n := 5 // default value
	lcp := c.SourceListLineCount
	if lcp != nil && *lcp >= 0 {
		n = *lcp
	}
	return n
}

func (c *Config) GetDisassembleFlavour() api.AssemblyFlavour {
	if c == nil || c.DisassembleFlavor == nil {
		return api.IntelFlavour
	}
	switch *c.DisassembleFlavor {
	case "go":
		return api.GoFlavour
	case "gnu":
		return api.GNUFlavour
	default:
		return api.IntelFlavour
	}
}

// LoadConfig attempts to populate a Config object from the config.yml file.
func LoadConfig() (*Config, error) {
	err := createConfigPath()
	if err != nil {
		return &Config{}, fmt.Errorf("could not create config directory: %v", err)
	}
	fullConfigFile, err := GetConfigFilePath(configFile)
	if err != nil {
		return &Config{}, fmt.Errorf("unable to get config file path: %v", err)
	}

	hasOldConfig, _ := hasOldConfig()

	if hasOldConfig {
		userHomeDir := getUserHomeDir()
		oldLocation := path.Join(userHomeDir, configDirHidden)
		if err := moveOldConfig(); err != nil {
			return &Config{}, fmt.Errorf("unable to move old config: %v", err)
		}

		if err := os.RemoveAll(oldLocation); err != nil {
			return &Config{}, fmt.Errorf("unable to remove old config location: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Successfully moved config from: %s to: %s\n", oldLocation, fullConfigFile)
	}

	f, err := os.Open(fullConfigFile)
	if err != nil {
		f, err = createDefaultConfig(fullConfigFile)
		if err != nil {
			return &Config{}, fmt.Errorf("error creating default config file: %v", err)
		}
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return &Config{}, fmt.Errorf("unable to read config data: %v", err)
	}

	var c Config
	err = yaml.Unmarshal(data, &c)
	if err != nil {
		return &Config{}, fmt.Errorf("unable to decode config file: %v", err)
	}

	if len(c.DebugInfoDirectories) == 0 {
		c.DebugInfoDirectories = []string{"/usr/lib/debug/.build-id"}
	}

	return &c, nil
}

// SaveConfig will marshal and save the config struct
// to disk.
func SaveConfig(conf *Config) error {
	fullConfigFile, err := GetConfigFilePath(configFile)
	if err != nil {
		return err
	}

	out, err := yaml.Marshal(*conf)
	if err != nil {
		return err
	}

	f, err := os.Create(fullConfigFile)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(out)
	return err
}

// moveOldConfig attempts to move config to new location
// $HOME/.dlv to $XDG_CONFIG_HOME/dlv
func moveOldConfig() error {
	if os.Getenv("XDG_CONFIG_HOME") == "" && runtime.GOOS != "linux" {
		return nil
	}

	userHomeDir := getUserHomeDir()

	p := path.Join(userHomeDir, configDirHidden, configFile)
	_, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("unable to read config file located at: %s", p)
	}

	newFile, err := GetConfigFilePath(configFile)
	if err != nil {
		return fmt.Errorf("unable to read config file located at: %s", err)
	}

	if err := os.Rename(p, newFile); err != nil {
		return fmt.Errorf("unable to move %s to %s", p, newFile)
	}
	return nil
}

func createDefaultConfig(path string) (*os.File, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("unable to create config file: %v", err)
	}
	err = writeDefaultConfig(f)
	if err != nil {
		return nil, fmt.Errorf("unable to write default configuration: %v", err)
	}
	f.Seek(0, io.SeekStart)
	return f, nil
}

func writeDefaultConfig(f *os.File) error {
	_, err := f.WriteString(
		`# Configuration file for the delve debugger.

# This is the default configuration file. Available options are provided, but disabled.
# Delete the leading hash mark to enable an item.

# Uncomment the following line and set your preferred ANSI color for source
# line numbers in the (list) command. The default is 34 (dark blue). See
# https://en.wikipedia.org/wiki/ANSI_escape_code#3/4_bit
# source-list-line-color: "\x1b[34m"

# Uncomment the following lines to change the colors used by syntax highlighting.
# source-list-keyword-color: "\x1b[0m"
# source-list-string-color: "\x1b[92m"
# source-list-number-color: "\x1b[0m"
# source-list-comment-color: "\x1b[95m"
# source-list-arrow-color: "\x1b[93m"
# source-list-tab-color: "\x1b[90m"

# Uncomment to change what is printed instead of '\t'.
# tab: "... "

# Uncomment to change the number of lines printed above and below cursor when
# listing source code.
# source-list-line-count: 5

# Provided aliases will be added to the default aliases for a given command.
aliases:
  # command: ["alias1", "alias2"]

# Define sources path substitution rules. Can be used to rewrite a source path stored
# in program's debug information, if the sources were moved to a different place
# between compilation and debugging.
# Note that substitution rules will not be used for paths passed to "break" and "trace"
# commands.
# See also Documentation/cli/substitutepath.md.
substitute-path:
  # - {from: path, to: path}
  
# Maximum number of elements loaded from an array.
# max-array-values: 64

# Maximum loaded string length.
# max-string-len: 64

# Output evaluation.
# max-variable-recurse: 1

# Uncomment the following line to make the whatis command also print the DWARF location expression of its argument.
# show-location-expr: true

# Allow user to specify output syntax flavor of assembly, one of this list "intel"(default), "gnu", "go".
# disassemble-flavor: intel

# List of directories to use when searching for separate debug info files.
debug-info-directories: ["/usr/lib/debug/.build-id"]

# Uncomment to change how the current program position is displayed.
# Possible options
#  - 'source' always shows source code 
#  - 'disassembly' always show disassembly
#  - 'default' show disassembly after step-instruction, source otherwise
# position default
`)
	return err
}

// createConfigPath creates the directory structure at which all config files are saved.
func createConfigPath() error {
	path, err := GetConfigFilePath("")
	if err != nil {
		return err
	}
	return os.MkdirAll(path, 0700)
}

// GetConfigFilePath gets the full path to the given config file name.
func GetConfigFilePath(file string) (string, error) {
	if configPath := os.Getenv("XDG_CONFIG_HOME"); configPath != "" {
		return path.Join(configPath, configDir, file), nil
	}

	userHomeDir := getUserHomeDir()

	if runtime.GOOS == "linux" {
		return path.Join(userHomeDir, ".config", configDir, file), nil
	}
	return path.Join(userHomeDir, configDirHidden, file), nil
}

// Checks if the user has a config at the old location: $HOME/.dlv
func hasOldConfig() (bool, error) {
	// If you don't have XDG_CONFIG_HOME set and aren't on Linux you have nothing to move
	if os.Getenv("XDG_CONFIG_HOME") == "" && runtime.GOOS != "linux" {
		return false, nil
	}

	userHomeDir := getUserHomeDir()

	o := path.Join(userHomeDir, configDirHidden, configFile)
	_, err := os.Stat(o)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func getUserHomeDir() string {
	userHomeDir := "."
	usr, err := user.Current()
	if err == nil {
		userHomeDir = usr.HomeDir
	}
	return userHomeDir
}

func WriteConfigDocumentation(w io.Writer) {
	fmt.Fprint(w, "# Configuration\n\n")
	fmt.Fprint(w, "The configuration file `config.yml` is found in `$XDG_CONFIG_HOME/dlv` if `$XDG_CONFIG_HOME` is set, if it isn't set it will be in `$HOME/.config/dlv` on Linux and `$HOME/.dlv` in other operating systems.\n\n")
	fmt.Fprint(w, "The following options are available:\n\n")
	fmt.Fprint(w, "Option | Description\n")
	fmt.Fprint(w, "-------|------------\n")
	for _, name := range slices.Sorted(maps.Keys(Documentation)) {
		switch name {
		case "substitute-path":
			fmt.Fprint(w, "substitute-path | Path substitution rules, a list of `{ from: path, to: path }` pairs.\n")
		case "aliases":
			fmt.Fprint(w, "aliases | Map fo command aliases `command: [ \"alias1\", \"alias2\" ]`.\n")
		case "debug-info-directories":
			fmt.Fprint(w, "debug-info-directories | List of directories to use when searching for separate debug info files.\n")
		case "position":
			fmt.Fprint(w, "position | Controls how the current position in the program is displayed (source | disassembly | default).\n")
		default:
			doc := Documentation[name]
			if doc[len(doc)-1] == '\n' {
				doc = doc[:len(doc)-1]
			}
			fmt.Fprintf(w, "%s | %s\n", name, doc)
		}
	}
	fmt.Fprint(w, "\n")
}
