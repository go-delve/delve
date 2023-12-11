// This file is part of GoRE.
//
// Copyright (C) 2019-2021 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package gore

import (
	"bytes"
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	elfMagic       = []byte{0x7f, 0x45, 0x4c, 0x46}
	peMagic        = []byte{0x4d, 0x5a}
	maxMagicBufLen = 4
	machoMagic1    = []byte{0xfe, 0xed, 0xfa, 0xce}
	machoMagic2    = []byte{0xfe, 0xed, 0xfa, 0xcf}
	machoMagic3    = []byte{0xce, 0xfa, 0xed, 0xfe}
	machoMagic4    = []byte{0xcf, 0xfa, 0xed, 0xfe}
)

// Open opens a file and returns a handler to the file.
func Open(filePath string) (*GoFile, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	_, err = f.Seek(0, 0)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, maxMagicBufLen)
	n, err := f.Read(buf)
	f.Close()
	if err != nil {
		return nil, err
	}
	if n < maxMagicBufLen {
		return nil, ErrNotEnoughBytesRead
	}
	gofile := new(GoFile)
	if fileMagicMatch(buf, elfMagic) {
		elf, err := openELF(filePath)
		if err != nil {
			return nil, err
		}
		gofile.fh = elf
	} else if fileMagicMatch(buf, peMagic) {
		pe, err := openPE(filePath)
		if err != nil {
			return nil, err
		}
		gofile.fh = pe
	} else if fileMagicMatch(buf, machoMagic1) || fileMagicMatch(buf, machoMagic2) || fileMagicMatch(buf, machoMagic3) || fileMagicMatch(buf, machoMagic4) {
		macho, err := openMachO(filePath)
		if err != nil {
			return nil, err
		}
		gofile.fh = macho
	} else {
		return nil, ErrUnsupportedFile
	}
	gofile.FileInfo = gofile.fh.getFileInfo()

	// If the ID has been removed or tampered with, this will fail. If we can't
	// get a build ID, we skip it.
	buildID, err := gofile.fh.getBuildID()
	if err == nil {
		gofile.BuildID = buildID
	}

	// Try to extract build information.
	if bi, err := gofile.extractBuildInfo(); err == nil {
		// This error is a minor failure, it just means we don't have
		// this information. So if fails we just ignores it.
		gofile.BuildInfo = bi
		if bi.Compiler != nil {
			gofile.FileInfo.goversion = bi.Compiler
		}
	}

	return gofile, nil
}

// GoFile is a structure representing a go binary file.
type GoFile struct {
	// BuildInfo holds the data from the buildinf structure. This can be nil
	// because it's not always available.
	BuildInfo *BuildInfo
	// FileInfo holds information about the file.
	FileInfo *FileInfo
	// BuildID is the Go build ID hash extracted from the binary.
	BuildID      string
	fh           fileHandler
	stdPkgs      []*Package
	generated    []*Package
	pkgs         []*Package
	vendors      []*Package
	unknown      []*Package
	pclntab      *gosym.Table
	initPackages sync.Once
}

func (f *GoFile) init() error {
	var returnVal error
	f.initPackages.Do(func() {
		tab, err := f.PCLNTab()
		if err != nil {
			returnVal = err
			return
		}
		f.pclntab = tab
		returnVal = f.enumPackages()
	})
	return returnVal
}

// GetCompilerVersion returns the Go compiler version of the compiler
// that was used to compile the binary.
func (f *GoFile) GetCompilerVersion() (*GoVersion, error) {
	return findGoCompilerVersion(f)
}

// SourceInfo returns the source code filename, starting line number
// and ending line number for the function.
func (f *GoFile) SourceInfo(fn *Function) (string, int, int) {
	srcFile, _, _ := f.pclntab.PCToLine(fn.Offset)
	start, end := findSourceLines(fn.Offset, fn.End, f.pclntab)
	return srcFile, start, end
}

// GetGoRoot returns the Go Root path
// that was used to compile the binary.
func (f *GoFile) GetGoRoot() (string, error) {
	err := f.init()
	if err != nil {
		return "", err
	}
	return findGoRootPath(f)
}

// SetGoVersion sets the assumed compiler version that was used. This
// can be used to force a version if gore is not able to determine the
// compiler version used. The version string must match one of the strings
// normally extracted from the binary. For example to set the version to
// go 1.12.0, use "go1.12". For 1.7.2, use "go1.7.2".
// If an incorrect version string or version not known to the library,
// ErrInvalidGoVersion is returned.
func (f *GoFile) SetGoVersion(version string) error {
	gv := ResolveGoVersion(version)
	if gv == nil {
		return ErrInvalidGoVersion
	}
	f.FileInfo.goversion = gv
	return nil
}

// GetPackages returns the go packages that has been classified as part of the main
// project.
func (f *GoFile) GetPackages() ([]*Package, error) {
	err := f.init()
	return f.pkgs, err
}

// GetVendors returns the 3rd party packages used by the binary.
func (f *GoFile) GetVendors() ([]*Package, error) {
	err := f.init()
	return f.vendors, err
}

// GetSTDLib returns the standard library packages used by the binary.
func (f *GoFile) GetSTDLib() ([]*Package, error) {
	err := f.init()
	return f.stdPkgs, err
}

// GetGeneratedPackages returns the compiler generated packages used by the binary.
func (f *GoFile) GetGeneratedPackages() ([]*Package, error) {
	err := f.init()
	return f.generated, err
}

// GetUnknown returns unclassified packages used by the binary. This is a catch all
// category when the classification could not be determined.
func (f *GoFile) GetUnknown() ([]*Package, error) {
	err := f.init()
	return f.unknown, err
}

func (f *GoFile) enumPackages() error {
	tab := f.pclntab
	packages := make(map[string]*Package)
	allPackages := sort.StringSlice{}

	for _, n := range tab.Funcs {
		needFilepath := false

		p, ok := packages[n.PackageName()]
		if !ok {
			p = &Package{
				Filepath:  filepath.Dir(n.BaseName()),
				Functions: make([]*Function, 0),
				Methods:   make([]*Method, 0),
			}
			packages[n.PackageName()] = p
			allPackages = append(allPackages, n.PackageName())
			needFilepath = true
		}

		if n.ReceiverName() != "" {
			m := &Method{
				Function: &Function{
					Name:        n.BaseName(),
					Offset:      n.Entry,
					End:         n.End,
					PackageName: n.PackageName(),
				},
				Receiver: n.ReceiverName(),
			}

			p.Methods = append(p.Methods, m)

			if !ok && needFilepath {
				fp, _, _ := tab.PCToLine(m.Offset)
				p.Filepath = filepath.Dir(fp)
			}
		} else {
			f := &Function{
				Name:        n.BaseName(),
				Offset:      n.Entry,
				End:         n.End,
				PackageName: n.PackageName(),
			}
			p.Functions = append(p.Functions, f)

			if !ok && needFilepath {
				fp, _, _ := tab.PCToLine(f.Offset)
				p.Filepath = filepath.Dir(fp)
			}
		}
	}

	allPackages.Sort()

	var classifier PackageClassifier

	if f.BuildInfo != nil && f.BuildInfo.ModInfo != nil {
		classifier = NewModPackageClassifier(f.BuildInfo.ModInfo)
	} else {
		mainPkg, ok := packages["main"]
		if !ok {
			return fmt.Errorf("no main package found")
		}

		classifier = NewPathPackageClassifier(mainPkg.Filepath)
	}

	for n, p := range packages {
		p.Name = n
		class := classifier.Classify(p)
		switch class {
		case ClassSTD:
			f.stdPkgs = append(f.stdPkgs, p)
		case ClassVendor:
			f.vendors = append(f.vendors, p)
		case ClassMain:
			f.pkgs = append(f.pkgs, p)
		case ClassUnknown:
			f.unknown = append(f.unknown, p)
		case ClassGenerated:
			f.generated = append(f.generated, p)
		}
	}
	return nil
}

// Close releases the file handler.
func (f *GoFile) Close() error {
	return f.fh.Close()
}

// PCLNTab returns the PCLN table.
func (f *GoFile) PCLNTab() (*gosym.Table, error) {
	return f.fh.getPCLNTab()
}

// GetTypes returns a map of all types found in the binary file.
func (f *GoFile) GetTypes() ([]*GoType, error) {
	if f.FileInfo.goversion == nil {
		ver, err := f.GetCompilerVersion()
		if err != nil {
			return nil, err
		}
		f.FileInfo.goversion = ver
	}
	t, err := getTypes(f.FileInfo, f.fh)
	if err != nil {
		return nil, err
	}
	if err = f.init(); err != nil {
		return nil, err
	}
	return sortTypes(t), nil
}

// Bytes returns a slice of raw bytes with the length in the file from the address.
func (f *GoFile) Bytes(address uint64, length uint64) ([]byte, error) {
	base, section, err := f.fh.getSectionDataFromOffset(address)
	if err != nil {
		return nil, err
	}

	if address+length-base > uint64(len(section)) {
		return nil, errors.New("length out of bounds")
	}

	return section[address-base : address+length-base], nil
}

func sortTypes(types map[uint64]*GoType) []*GoType {
	sortedList := make([]*GoType, len(types))

	i := 0
	for _, typ := range types {
		sortedList[i] = typ
		i++
	}
	sort.Slice(sortedList, func(i, j int) bool {
		if sortedList[i].PackagePath == sortedList[j].PackagePath {
			return sortedList[i].Name < sortedList[j].Name
		}
		return sortedList[i].PackagePath < sortedList[j].PackagePath
	})
	return sortedList
}

type fileHandler interface {
	io.Closer
	getPCLNTab() (*gosym.Table, error)
	getRData() ([]byte, error)
	getCodeSection() ([]byte, error)
	getSectionDataFromOffset(uint64) (uint64, []byte, error)
	getSectionData(string) (uint64, []byte, error)
	getFileInfo() *FileInfo
	getPCLNTABData() (uint64, []byte, error)
	moduledataSection() string
	getBuildID() (string, error)
	getFile() *os.File
}

func fileMagicMatch(buf, magic []byte) bool {
	return bytes.HasPrefix(buf, magic)
}

// FileInfo holds information about the file.
type FileInfo struct {
	// Arch is the architecture the binary is compiled for.
	Arch string
	// OS is the operating system the binary is compiled for.
	OS string
	// ByteOrder is the byte order.
	ByteOrder binary.ByteOrder
	// WordSize is the natural integer size used by the file.
	WordSize  int
	goversion *GoVersion
}

const (
	ArchAMD64 = "amd64"
	ArchARM   = "arm"
	ArchARM64 = "arm64"
	Arch386   = "i386"
	ArchMIPS  = "mips"
)
