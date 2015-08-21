package config

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"

	"gopkg.in/yaml.v2"
)

const (
	configDir  string = ".dlv"
	configFile string = "config.yml"
)

// Config defines all configuration options available to be set through the config file.
type Config struct {
	Aliases map[string][]string
}

// LoadConfig attempts to populate a Config object from the config.yml file.
func LoadConfig() *Config {
	err := createConfigPath()
	if err != nil {
		fmt.Printf("Could not create config directory: %v.")
		return nil
	}
	fullConfigFile, err := GetConfigFilePath(configFile)
	if err != nil {
		fmt.Printf("Unable to get config file path: %v.", err)
		return nil
	}

	f, err := os.Open(fullConfigFile)
	if err != nil {
		createDefaultConfig(fullConfigFile)
		return nil
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
		return nil
	}

	var c Config
	err = yaml.Unmarshal(data, &c)
	if err != nil {
		fmt.Printf("Unable to decode config file: %v.", err)
		return nil
	}

	return &c
}

func createDefaultConfig(path string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Unable to create config file: %v.", err)
		return
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("Closing config file failed: %v.", err)
		}
	}()
	err = writeDefaultConfig(f)
	if err != nil {
		fmt.Printf("Unable to write default configuration: %v.", err)
	}
}

func writeDefaultConfig(f *os.File) error {
	var buffer bytes.Buffer
	buffer.WriteString(
		`# Configuration file for the delve debugger.

# This is the default configuration file. Available options are provided, but disabled.
# Delete the leading hash mark to enable an item.

# Provided aliases will be added to the default aliases for a given command.
aliases:
  # command: ["alias1", "alias2"]
`)

	_, err := buffer.WriteTo(f)

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
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return path.Join(usr.HomeDir, configDir, file), nil
}
