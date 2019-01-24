package config

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/user"
	"path"
	"runtime"

	"gopkg.in/yaml.v2"
)

const (
	configDir       string = "dlv"
	configDirHidden string = ".dlv"
	configFile      string = "config.yml"
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

	// If ShowLocationExpr is true whatis will print the DWARF location
	// expression for its argument.
	ShowLocationExpr bool `yaml:"show-location-expr"`

	// Source list line-number color (3/4 bit color codes as defined
	// here: https://en.wikipedia.org/wiki/ANSI_escape_code#Colors)
	SourceListLineColor int `yaml:"source-list-line-color"`

	// DebugFileDirectories is the list of directories Delve will use
	// in order to resolve external debug info files.
	DebugInfoDirectories []string `yaml:"debug-info-directories"`
}

// LoadConfig attempts to populate a Config object from the config.yml file.
func LoadConfig() *Config {
	err := createConfigPath()
	if err != nil {
		fmt.Printf("Could not create config directory: %v.", err)
		return &Config{}
	}
	fullConfigFile, err := GetConfigFilePath(configFile)
	if err != nil {
		fmt.Printf("Unable to get config file path: %v.", err)
		return &Config{}
	}

	h, err := hasOldConfig()
	if err != nil {
		fmt.Printf("Unable to determine if old config exists: %s", err)
	}

	if h {
		userHomeDir := getUserHomeDir()
		oldLocation := path.Join(userHomeDir, configDirHidden)
		if err := moveOldConfig(); err != nil {
			fmt.Printf("Unable to move old config: %v.\n", err)
			return &Config{}
		}

		if err := os.RemoveAll(oldLocation); err != nil {
			fmt.Printf("Unable to remove old config location: %s", err)
			return &Config{}
		}
		fmt.Printf("Successfully moved config from: %s to: %s\n", oldLocation, fullConfigFile)
	}

	f, err := os.Open(fullConfigFile)
	if err != nil {
		f, err = createDefaultConfig(fullConfigFile)
		if err != nil {
			fmt.Printf("Error creating default config file: %v", err)
			return &Config{}
		}
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("Closing config file failed: %v.", err)
		}
	}()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		fmt.Printf("Unable to read config data: %v.", err)
		return &Config{}
	}

	var c Config
	err = yaml.Unmarshal(data, &c)
	if err != nil {
		fmt.Printf("Unable to decode config file: %v.", err)
		return &Config{}
	}

	if len(c.DebugInfoDirectories) == 0 {
		c.DebugInfoDirectories = []string{"/usr/lib/debug/.build-id"}
	}

	return &c
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
	file, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("unable to read config file located at: %s", p)
	}

	newFile, err := GetConfigFilePath(configFile)
	if err != nil {
		return fmt.Errorf("unable to read config file located at: %s", err)
	}

	if err := os.Rename(p, newFile); err != nil {
		return fmt.Errorf("unable to move %s to %s", file, newFile)
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

# Uncomment the following line and set your preferred ANSI foreground color
# for source line numbers in the (list) command (if unset, default is 34,
# dark blue) See https://en.wikipedia.org/wiki/ANSI_escape_code#3/4_bit
# source-list-line-color: 34

# Provided aliases will be added to the default aliases for a given command.
aliases:
  # command: ["alias1", "alias2"]

# Define sources path substitution rules. Can be used to rewrite a source path stored
# in program's debug information, if the sources were moved to a different place
# between compilation and debugging.
# Note that substitution rules will not be used for paths passed to "break" and "trace"
# commands.
substitute-path:
  # - {from: path, to: path}
  
# Maximum number of elements loaded from an array.
# max-array-values: 64

# Maximum loaded string length.
# max-string-len: 64

# Uncomment the following line to make the whatis command also print the DWARF location expression of its argument.
# show-location-expr: true

# List of directories to use when searching for separate debug info files.
debug-info-directories: ["/usr/lib/debug/.build-id"]
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
