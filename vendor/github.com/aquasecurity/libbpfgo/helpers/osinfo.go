package helpers

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type OSReleaseID uint32

func (o OSReleaseID) String() string {
	return osReleaseIDToString[o]
}

const (
	UBUNTU OSReleaseID = iota + 1
	FEDORA
	ARCH
	DEBIAN
	CENTOS
	STREAM
	ALMA
)

// stringToOSReleaseID is a map of supported distributions
var stringToOSReleaseID = map[string]OSReleaseID{
	"ubuntu": UBUNTU,
	"fedora": FEDORA,
	"arch":   ARCH,
	"debian": DEBIAN,
	"centos": CENTOS,
	"stream": STREAM,
	"alma":   ALMA,
}

// osReleaseIDToString is a map of supported distributions
var osReleaseIDToString = map[OSReleaseID]string{
	UBUNTU: "ubuntu",
	FEDORA: "fedora",
	ARCH:   "arch",
	DEBIAN: "debian",
	CENTOS: "centos",
	STREAM: "stream",
	ALMA:   "alma",
}

const (
	OS_NAME OSReleaseField = iota + 0
	OS_ID
	OS_ID_LIKE
	OS_PRETTY_NAME
	OS_VARIANT
	OS_VARIANT_ID
	OS_VERSION
	OS_VERSION_ID
	OS_VERSION_CODENAME
	OS_BUILD_ID
	OS_IMAGE_ID
	OS_IMAGE_VERSION
	OS_KERNEL_RELEASE // not part of default os-release, but we can use it here to facilitate things
)

type OSReleaseField uint32

func (o OSReleaseField) String() string {
	return osReleaseFieldToString[o]
}

// stringToOSReleaseField is a map of os-release file fields
var stringToOSReleaseField = map[string]OSReleaseField{
	"NAME":             OS_NAME,
	"ID":               OS_ID,
	"ID_LIKE":          OS_ID_LIKE,
	"PRETTY_NAME":      OS_PRETTY_NAME,
	"VARIANT":          OS_VARIANT,
	"VARIANT_ID":       OS_VARIANT_ID,
	"VERSION":          OS_VERSION,
	"VERSION_ID":       OS_VERSION_ID,
	"VERSION_CODENAME": OS_VERSION_CODENAME,
	"BUILD_ID":         OS_BUILD_ID,
	"IMAGE_ID":         OS_IMAGE_ID,
	"IMAGE_VERSION":    OS_IMAGE_VERSION,
	"KERNEL_RELEASE":   OS_KERNEL_RELEASE,
}

// osReleaseFieldToString is a map of os-release file fields
var osReleaseFieldToString = map[OSReleaseField]string{
	OS_NAME:             "NAME",
	OS_ID:               "ID",
	OS_ID_LIKE:          "ID_LIKE",
	OS_PRETTY_NAME:      "PRETTY_NAME",
	OS_VARIANT:          "VARIANT",
	OS_VARIANT_ID:       "VARIANT_ID",
	OS_VERSION:          "VERSION",
	OS_VERSION_ID:       "VERSION_ID",
	OS_VERSION_CODENAME: "VERSION_CODENAME",
	OS_BUILD_ID:         "BUILD_ID",
	OS_IMAGE_ID:         "IMAGE_ID",
	OS_IMAGE_VERSION:    "IMAGE_VERSION",
	OS_KERNEL_RELEASE:   "KERNEL_RELEASE",
}

// OSBTFEnabled checks if kernel has embedded BTF vmlinux file
func OSBTFEnabled() bool {
	_, err := os.Stat("/sys/kernel/btf/vmlinux") // TODO: accept a KernelConfig param and check for CONFIG_DEBUG_INFO_BTF=y, or similar

	return err == nil
}

// GetOSInfo creates a OSInfo object and runs discoverOSDistro() on its creation
func GetOSInfo() (*OSInfo, error) {
	info := OSInfo{}
	var err error

	if info.osReleaseFieldValues == nil {
		info.osReleaseFieldValues = make(map[OSReleaseField]string)
	}

	info.osReleaseFieldValues[OS_KERNEL_RELEASE], err = UnameRelease()
	if err != nil {
		return &info, fmt.Errorf("could not determine uname release: %w", err)
	}

	info.osReleaseFilePath, err = checkEnvPath("LIBBPFGO_OSRELEASE_FILE") // useful if users wants to mount host os-release in a container
	if err != nil {
		return &info, err
	} else if info.osReleaseFilePath == "" {
		info.osReleaseFilePath = "/etc/os-release"
	}

	if err = info.discoverOSDistro(); err != nil {
		return &info, err
	}

	return &info, nil
}

// OSInfo object contains all OS relevant information
//
// OSRelease is relevant to examples such as:
// 1) OSInfo.OSReleaseInfo[helpers.OS_KERNEL_RELEASE]) => will provide $(uname -r) string
// 2) if OSInfo.GetReleaseID() == helpers.UBUNTU => {} will allow to run code in specific distribution
//
type OSInfo struct {
	osReleaseFieldValues map[OSReleaseField]string
	osReleaseID          OSReleaseID
	osReleaseFilePath    string
}

// GetOSReleaseFieldValue provides access to internal OSInfo OSReleaseField's
func (btfi *OSInfo) GetOSReleaseFieldValue(value OSReleaseField) string {
	return btfi.osReleaseFieldValues[value]
}

// GetOSReleaseFilePath provides the path for the used os-release file as it might
// not necessarily be /etc/os-release, depending on the environment variable
func (btfi *OSInfo) GetOSReleaseFilePath() string {
	return btfi.osReleaseFilePath
}

// GetOSReleaseFilePath provides the ID of current Linux distribution
func (btfi *OSInfo) GetOSReleaseID() OSReleaseID {
	return btfi.osReleaseID
}

// GetOSReleaseAllFieldValues allows user to dump, as strings, the existing OSReleaseField's and its values
func (btfi *OSInfo) GetOSReleaseAllFieldValues() map[OSReleaseField]string {
	summary := make(map[OSReleaseField]string)

	for k, v := range btfi.osReleaseFieldValues {
		summary[k] = v // create a copy so consumer can read internal data (e.g. debugging)
	}

	return summary
}

// CompareOSBaseKernelRelease will compare a given kernel version/release string
// to the current running version and return -1, 0 or 1 if given version is less,
// equal or bigger, respectively, than running one. Example:
//
// OSInfo.CompareOSBaseKernelRelease("5.11.0"))
//
func (btfi *OSInfo) CompareOSBaseKernelRelease(version string) int {
	return CompareKernelRelease(btfi.osReleaseFieldValues[OS_KERNEL_RELEASE], version)
}

// discoverOSDistro discover running Linux distribution information by reading UTS and
// the /etc/os-releases file (https://man7.org/linux/man-pages/man5/os-release.5.html)
func (btfi *OSInfo) discoverOSDistro() error {
	var err error

	if btfi.osReleaseFilePath == "" {
		return fmt.Errorf("should specify os-release filepath")
	}

	file, err := os.Open(btfi.osReleaseFilePath)
	if err != nil {
		return err
	}

	defer file.Close()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		val := strings.Split(scanner.Text(), "=")
		if len(val) != 2 {
			continue
		}
		keyID := stringToOSReleaseField[val[0]]
		if keyID == 0 { // could not find KEY= from os-release in consts
			continue
		}
		btfi.osReleaseFieldValues[keyID] = val[1]
		if keyID == OS_ID {
			btfi.osReleaseID = stringToOSReleaseID[strings.ToLower(val[1])]
		}
	}

	return nil
}
