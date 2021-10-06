package helpers

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// KernelConfigOption is an abstraction of the key in key=value syntax of the kernel config file
type KernelConfigOption uint32

// KernelConfigOptionValue is an abstraction of the value in key=value syntax of kernel config file
type KernelConfigOptionValue uint8

const (
	UNDEFINED KernelConfigOptionValue = iota
	BUILTIN
	MODULE
	STRING
	ANY
)

func (k KernelConfigOption) String() string {
	return kernelConfigKeyIDToString[k]
}

func (k KernelConfigOptionValue) String() string {
	switch k {
	case UNDEFINED:
		return "UNDEFINED"
	case BUILTIN:
		return "BUILTIN"
	case MODULE:
		return "MODULE"
	case STRING:
		return "STRING"
	case ANY:
		return "ANY"
	}

	return ""
}

// These constants are a limited number of the total kernel config options,
// but are provided because they are most relevant for BPF development.

const (
	CONFIG_BPF KernelConfigOption = iota + 1
	CONFIG_BPF_SYSCALL
	CONFIG_HAVE_EBPF_JIT
	CONFIG_BPF_JIT
	CONFIG_BPF_JIT_ALWAYS_ON
	CONFIG_CGROUPS
	CONFIG_CGROUP_BPF
	CONFIG_CGROUP_NET_CLASSID
	CONFIG_SOCK_CGROUP_DATA
	CONFIG_BPF_EVENTS
	CONFIG_KPROBE_EVENTS
	CONFIG_UPROBE_EVENTS
	CONFIG_TRACING
	CONFIG_FTRACE_SYSCALLS
	CONFIG_FUNCTION_ERROR_INJECTION
	CONFIG_BPF_KPROBE_OVERRIDE
	CONFIG_NET
	CONFIG_XDP_SOCKETS
	CONFIG_LWTUNNEL_BPF
	CONFIG_NET_ACT_BPF
	CONFIG_NET_CLS_BPF
	CONFIG_NET_CLS_ACT
	CONFIG_NET_SCH_INGRESS
	CONFIG_XFRM
	CONFIG_IP_ROUTE_CLASSID
	CONFIG_IPV6_SEG6_BPF
	CONFIG_BPF_LIRC_MODE2
	CONFIG_BPF_STREAM_PARSER
	CONFIG_NETFILTER_XT_MATCH_BPF
	CONFIG_BPFILTER
	CONFIG_BPFILTER_UMH
	CONFIG_TEST_BPF
	CONFIG_HZ
	CONFIG_DEBUG_INFO_BTF
	CONFIG_DEBUG_INFO_BTF_MODULES
	CONFIG_BPF_LSM
	CONFIG_BPF_PRELOAD
	CONFIG_BPF_PRELOAD_UMD
	CUSTOM_OPTION_START KernelConfigOption = 1000
)

var kernelConfigKeyStringToID = map[string]KernelConfigOption{
	"CONFIG_BPF":                      CONFIG_BPF,
	"CONFIG_BPF_SYSCALL":              CONFIG_BPF_SYSCALL,
	"CONFIG_HAVE_EBPF_JIT":            CONFIG_HAVE_EBPF_JIT,
	"CONFIG_BPF_JIT":                  CONFIG_BPF_JIT,
	"CONFIG_BPF_JIT_ALWAYS_ON":        CONFIG_BPF_JIT_ALWAYS_ON,
	"CONFIG_CGROUPS":                  CONFIG_CGROUPS,
	"CONFIG_CGROUP_BPF":               CONFIG_CGROUP_BPF,
	"CONFIG_CGROUP_NET_CLASSID":       CONFIG_CGROUP_NET_CLASSID,
	"CONFIG_SOCK_CGROUP_DATA":         CONFIG_SOCK_CGROUP_DATA,
	"CONFIG_BPF_EVENTS":               CONFIG_BPF_EVENTS,
	"CONFIG_KPROBE_EVENTS":            CONFIG_KPROBE_EVENTS,
	"CONFIG_UPROBE_EVENTS":            CONFIG_UPROBE_EVENTS,
	"CONFIG_TRACING":                  CONFIG_TRACING,
	"CONFIG_FTRACE_SYSCALLS":          CONFIG_FTRACE_SYSCALLS,
	"CONFIG_FUNCTION_ERROR_INJECTION": CONFIG_FUNCTION_ERROR_INJECTION,
	"CONFIG_BPF_KPROBE_OVERRIDE":      CONFIG_BPF_KPROBE_OVERRIDE,
	"CONFIG_NET":                      CONFIG_NET,
	"CONFIG_XDP_SOCKETS":              CONFIG_XDP_SOCKETS,
	"CONFIG_LWTUNNEL_BPF":             CONFIG_LWTUNNEL_BPF,
	"CONFIG_NET_ACT_BPF":              CONFIG_NET_ACT_BPF,
	"CONFIG_NET_CLS_BPF":              CONFIG_NET_CLS_BPF,
	"CONFIG_NET_CLS_ACT":              CONFIG_NET_CLS_ACT,
	"CONFIG_NET_SCH_INGRESS":          CONFIG_NET_SCH_INGRESS,
	"CONFIG_XFRM":                     CONFIG_XFRM,
	"CONFIG_IP_ROUTE_CLASSID":         CONFIG_IP_ROUTE_CLASSID,
	"CONFIG_IPV6_SEG6_BPF":            CONFIG_IPV6_SEG6_BPF,
	"CONFIG_BPF_LIRC_MODE2":           CONFIG_BPF_LIRC_MODE2,
	"CONFIG_BPF_STREAM_PARSER":        CONFIG_BPF_STREAM_PARSER,
	"CONFIG_NETFILTER_XT_MATCH_BPF":   CONFIG_NETFILTER_XT_MATCH_BPF,
	"CONFIG_BPFILTER":                 CONFIG_BPFILTER,
	"CONFIG_BPFILTER_UMH":             CONFIG_BPFILTER_UMH,
	"CONFIG_TEST_BPF":                 CONFIG_TEST_BPF,
	"CONFIG_HZ":                       CONFIG_HZ,
	"CONFIG_DEBUG_INFO_BTF":           CONFIG_DEBUG_INFO_BTF,
	"CONFIG_DEBUG_INFO_BTF_MODULES":   CONFIG_DEBUG_INFO_BTF_MODULES,
	"CONFIG_BPF_LSM":                  CONFIG_BPF_LSM,
	"CONFIG_BPF_PRELOAD":              CONFIG_BPF_PRELOAD,
	"CONFIG_BPF_PRELOAD_UMD":          CONFIG_BPF_PRELOAD_UMD,
}

var kernelConfigKeyIDToString = map[KernelConfigOption]string{
	CONFIG_BPF:                      "CONFIG_BPF",
	CONFIG_BPF_SYSCALL:              "CONFIG_BPF_SYSCALL",
	CONFIG_HAVE_EBPF_JIT:            "CONFIG_HAVE_EBPF_JIT",
	CONFIG_BPF_JIT:                  "CONFIG_BPF_JIT",
	CONFIG_BPF_JIT_ALWAYS_ON:        "CONFIG_BPF_JIT_ALWAYS_ON",
	CONFIG_CGROUPS:                  "CONFIG_CGROUPS",
	CONFIG_CGROUP_BPF:               "CONFIG_CGROUP_BPF",
	CONFIG_CGROUP_NET_CLASSID:       "CONFIG_CGROUP_NET_CLASSID",
	CONFIG_SOCK_CGROUP_DATA:         "CONFIG_SOCK_CGROUP_DATA",
	CONFIG_BPF_EVENTS:               "CONFIG_BPF_EVENTS",
	CONFIG_KPROBE_EVENTS:            "CONFIG_KPROBE_EVENTS",
	CONFIG_UPROBE_EVENTS:            "CONFIG_UPROBE_EVENTS",
	CONFIG_TRACING:                  "CONFIG_TRACING",
	CONFIG_FTRACE_SYSCALLS:          "CONFIG_FTRACE_SYSCALLS",
	CONFIG_FUNCTION_ERROR_INJECTION: "CONFIG_FUNCTION_ERROR_INJECTION",
	CONFIG_BPF_KPROBE_OVERRIDE:      "CONFIG_BPF_KPROBE_OVERRIDE",
	CONFIG_NET:                      "CONFIG_NET",
	CONFIG_XDP_SOCKETS:              "CONFIG_XDP_SOCKETS",
	CONFIG_LWTUNNEL_BPF:             "CONFIG_LWTUNNEL_BPF",
	CONFIG_NET_ACT_BPF:              "CONFIG_NET_ACT_BPF",
	CONFIG_NET_CLS_BPF:              "CONFIG_NET_CLS_BPF",
	CONFIG_NET_CLS_ACT:              "CONFIG_NET_CLS_ACT",
	CONFIG_NET_SCH_INGRESS:          "CONFIG_NET_SCH_INGRESS",
	CONFIG_XFRM:                     "CONFIG_XFRM",
	CONFIG_IP_ROUTE_CLASSID:         "CONFIG_IP_ROUTE_CLASSID",
	CONFIG_IPV6_SEG6_BPF:            "CONFIG_IPV6_SEG6_BPF",
	CONFIG_BPF_LIRC_MODE2:           "CONFIG_BPF_LIRC_MODE2",
	CONFIG_BPF_STREAM_PARSER:        "CONFIG_BPF_STREAM_PARSER",
	CONFIG_NETFILTER_XT_MATCH_BPF:   "CONFIG_NETFILTER_XT_MATCH_BPF",
	CONFIG_BPFILTER:                 "CONFIG_BPFILTER",
	CONFIG_BPFILTER_UMH:             "CONFIG_BPFILTER_UMH",
	CONFIG_TEST_BPF:                 "CONFIG_TEST_BPF",
	CONFIG_HZ:                       "CONFIG_HZ",
	CONFIG_DEBUG_INFO_BTF:           "CONFIG_DEBUG_INFO_BTF",
	CONFIG_DEBUG_INFO_BTF_MODULES:   "CONFIG_DEBUG_INFO_BTF_MODULES",
	CONFIG_BPF_LSM:                  "CONFIG_BPF_LSM",
	CONFIG_BPF_PRELOAD:              "CONFIG_BPF_PRELOAD",
	CONFIG_BPF_PRELOAD_UMD:          "CONFIG_BPF_PRELOAD_UMD",
}

// KernelConfig is a set of kernel configuration options (currently for running OS only)
type KernelConfig struct {
	configs         map[KernelConfigOption]interface{} // predominantly KernelConfigOptionValue, sometimes string
	needed          map[KernelConfigOption]interface{}
	kConfigFilePath string
}

// InitKernelConfig inits external KernelConfig object
func InitKernelConfig() (*KernelConfig, error) {
	config := KernelConfig{}

	// special case: user provided kconfig file (it MUST exist)

	osKConfigFilePath, err := checkEnvPath("LIBBPFGO_KCONFIG_FILE") // override /proc/config.gz or /boot/config-$(uname -r) if needed (containers)
	if err != nil {
		return &config, err
	}
	if len(osKConfigFilePath) > 2 {
		if _, err := os.Stat(osKConfigFilePath); err != nil {
			return &config, err
		}
		config.kConfigFilePath = osKConfigFilePath
		if err := config.initKernelConfig(osKConfigFilePath); err != nil {
			return &config, err
		}

		return &config, nil
	}

	// fastpath: check config.gz in procfs first

	configGZ := "/proc/config.gz"
	if _, err1 := os.Stat(configGZ); err1 == nil {
		config.kConfigFilePath = configGZ
		if err2 := config.initKernelConfig(configGZ); err2 != nil {
			return &config, err2
		}

		return &config, nil
	} // ignore if /proc/config.gz does not exist

	// slowerpath: /boot/$(uname -r)

	releaseVersion, err := UnameRelease()
	if err != nil {
		return &config, err
	}

	releaseFilePath := fmt.Sprintf("/boot/config-%s", releaseVersion)
	config.kConfigFilePath = releaseFilePath
	err = config.initKernelConfig(releaseFilePath)

	return &config, err
}

// GetKernelConfigFilePath gives the kconfig file chosen by InitKernelConfig during initialization
func (k *KernelConfig) GetKernelConfigFilePath() string {
	return k.kConfigFilePath
}

// AddCustomKernelConfig allows user to extend list of possible existing kconfigs to be parsed from kConfigFilePath
func (k *KernelConfig) AddCustomKernelConfig(key KernelConfigOption, value string) error {
	if key < CUSTOM_OPTION_START {
		return fmt.Errorf("KConfig key index must be bigger than %d (CUSTOM_OPTION_START)\n", CUSTOM_OPTION_START)
	}

	// extend initial list of kconfig options: add other possible existing ones
	kernelConfigKeyIDToString[key] = value
	kernelConfigKeyStringToID[value] = key

	return nil
}

// LoadKernelConfig will (re)read kconfig file (likely after AddCustomKernelConfig was called)
func (k *KernelConfig) LoadKernelConfig() error {
	return k.initKernelConfig(k.kConfigFilePath)
}

// initKernelConfig inits internal KernelConfig data by calling appropriate readConfigFromXXX function
func (k *KernelConfig) initKernelConfig(configFilePath string) error {
	if _, err := os.Stat(configFilePath); err != nil {
		return fmt.Errorf("could not read %v: %w", configFilePath, err)
	}

	if strings.Compare(filepath.Ext(configFilePath), ".gz") == 0 {
		return k.readConfigFromProcConfigGZ(configFilePath)
	}

	return k.readConfigFromBootConfigRelease(configFilePath) // assume it is a txt file by default
}

// readConfigFromBootConfigRelease prepares io.Reader (/boot/config-$(uname -r)) for readConfigFromScanner
func (k *KernelConfig) readConfigFromBootConfigRelease(filePath string) error {
	file, _ := os.Open(filePath) // already checked
	k.readConfigFromScanner(file)
	file.Close()

	return nil
}

// readConfigFromProcConfigGZ prepares gziped io.Reader (/proc/config.gz) for readConfigFromScanner
func (k *KernelConfig) readConfigFromProcConfigGZ(filePath string) error {
	file, _ := os.Open(filePath) // already checked
	zreader, _ := gzip.NewReader(file)
	k.readConfigFromScanner(zreader)
	zreader.Close()
	file.Close()

	return nil
}

// readConfigFromScanner reads all existing KernelConfigOption's and KernelConfigOptionValue's from given io.Reader
func (k *KernelConfig) readConfigFromScanner(reader io.Reader) {

	if k.configs == nil {
		k.configs = make(map[KernelConfigOption]interface{})
	}
	if k.needed == nil {
		k.needed = make(map[KernelConfigOption]interface{})
	}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		kv := strings.Split(scanner.Text(), "=")
		if len(kv) != 2 {
			continue
		}

		configKeyID := kernelConfigKeyStringToID[kv[0]]
		if configKeyID == 0 {
			continue
		}
		if strings.Compare(kv[1], "m") == 0 {
			k.configs[configKeyID] = MODULE
		} else if strings.Compare(kv[1], "y") == 0 {
			k.configs[configKeyID] = BUILTIN
		} else {
			k.configs[configKeyID] = kv[1]
		}
	}
}

// GetValue will return a KernelConfigOptionValue for a given KernelConfigOption when this is a BUILTIN or a MODULE
func (k *KernelConfig) GetValue(option KernelConfigOption) KernelConfigOptionValue {
	value, ok := k.configs[KernelConfigOption(option)].(KernelConfigOptionValue)
	if ok {
		return value
	}

	return UNDEFINED // not an error as the config option might not exist in kconfig file
}

// GetValueString will return a KernelConfigOptionValue for a given KernelConfigOption when this is actually a string
func (k *KernelConfig) GetValueString(option KernelConfigOption) (string, error) {
	value, ok := k.configs[option].(string)
	if ok {
		return value, nil
	}

	return "", fmt.Errorf("given option's value (%s) is not a string", option)
}

// Exists will return true if a given KernelConfigOption was found in provided KernelConfig
// and it will return false if the KernelConfigOption is not set (# XXXXX is not set)
//
// Examples:
// kernelConfig.Exists(helpers.CONFIG_BPF)
// kernelConfig.Exists(helpers.CONFIG_BPF_PRELOAD)
// kernelConfig.Exists(helpers.CONFIG_HZ)
//
func (k *KernelConfig) Exists(option KernelConfigOption) bool {
	if _, ok := k.configs[option]; ok {
		return true
	}

	return false
}

// ExistsValue will return true if a given KernelConfigOption was found in provided KernelConfig
// AND its value is the same as the one provided by KernelConfigOptionValue
func (k *KernelConfig) ExistsValue(option KernelConfigOption, value interface{}) bool {
	if cfg, ok := k.configs[option]; ok {
		switch cfg.(type) {
		case KernelConfigOptionValue:
			if value == ANY {
				return true
			} else if k.configs[option].(KernelConfigOptionValue) == value {
				return true
			}
		case string:
			if strings.Compare(k.configs[option].(string), value.(string)) == 0 {
				return true
			}
		}
	}

	return false
}

// CheckMissing returns an array of KernelConfigOption's that were added to KernelConfig as needed but couldn't be
// found. It returns an empty array if nothing is missing.
func (k *KernelConfig) CheckMissing() []KernelConfigOption {
	missing := make([]KernelConfigOption, 0)

	for key, value := range k.needed {
		if !k.ExistsValue(key, value) {
			missing = append(missing, key)
		}
	}

	return missing
}

// AddNeeded adds a KernelConfigOption and its value, if needed, as required for further checks with CheckMissing
//
// Examples:
// kernelConfig.AddNeeded(helpers.CONFIG_BPF, helpers.ANY)
// kernelConfig.AddNeeded(helpers.CONFIG_BPF_PRELOAD, helpers.ANY)
// kernelConfig.AddNeeded(helpers.CONFIG_HZ, "250")
//
func (k *KernelConfig) AddNeeded(option KernelConfigOption, value interface{}) {
	if _, ok := kernelConfigKeyIDToString[option]; ok {
		k.needed[option] = value
	}
}
