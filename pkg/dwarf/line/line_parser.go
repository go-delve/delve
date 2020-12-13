package line

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/util"
)

// DebugLinePrologue prologue of .debug_line data.
type DebugLinePrologue struct {
	UnitLength     uint32
	Version        uint16
	Length         uint32
	MinInstrLength uint8
	MaxOpPerInstr  uint8
	InitialIsStmt  uint8
	LineBase       int8
	LineRange      uint8
	OpcodeBase     uint8
	StdOpLengths   []uint8
}

// DebugLineInfo info of .debug_line data.
type DebugLineInfo struct {
	Prologue     *DebugLinePrologue
	IncludeDirs  []string
	FileNames    []*FileEntry
	Instructions []byte
	Lookup       map[string]*FileEntry

	Logf func(string, ...interface{})

	// stateMachineCache[pc] is a state machine stopped at pc
	stateMachineCache map[uint64]*StateMachine

	// lastMachineCache[pc] is a state machine stopped at an address after pc
	lastMachineCache map[uint64]*StateMachine

	// staticBase is the address at which the executable is loaded, 0 for non-PIEs
	staticBase uint64

	// if normalizeBackslash is true all backslashes (\) will be converted into forward slashes (/)
	normalizeBackslash bool
	ptrSize            int
	endSeqIsValid      bool
}

// FileEntry file entry in File Name Table.
type FileEntry struct {
	Path        string
	DirIdx      uint64
	LastModTime uint64
	Length      uint64
}

type DebugLines []*DebugLineInfo

// ParseAll parses all debug_line segments found in data
func ParseAll(data []byte, logfn func(string, ...interface{}), staticBase uint64, normalizeBackslash bool, ptrSize int) DebugLines {
	var (
		lines = make(DebugLines, 0)
		buf   = bytes.NewBuffer(data)
	)

	// We have to parse multiple file name tables here.
	for buf.Len() > 0 {
		lines = append(lines, Parse("", buf, logfn, staticBase, normalizeBackslash, ptrSize))
	}

	return lines
}

// Parse parses a single debug_line segment from buf. Compdir is the
// DW_AT_comp_dir attribute of the associated compile unit.
func Parse(compdir string, buf *bytes.Buffer, logfn func(string, ...interface{}), staticBase uint64, normalizeBackslash bool, ptrSize int) *DebugLineInfo {
	dbl := new(DebugLineInfo)
	dbl.Logf = logfn
	dbl.staticBase = staticBase
	dbl.ptrSize = ptrSize
	dbl.Lookup = make(map[string]*FileEntry)
	dbl.IncludeDirs = append(dbl.IncludeDirs, compdir)

	dbl.stateMachineCache = make(map[uint64]*StateMachine)
	dbl.lastMachineCache = make(map[uint64]*StateMachine)
	dbl.normalizeBackslash = normalizeBackslash

	parseDebugLinePrologue(dbl, buf)
	if dbl.Prologue.Version >= 5 {
		parseIncludeDirs5(dbl, buf)
		parseFileEntries5(dbl, buf)
	} else {
		parseIncludeDirs2(dbl, buf)
		parseFileEntries2(dbl, buf)
	}

	// Instructions size calculation breakdown:
	//   - dbl.Prologue.UnitLength is the length of the entire unit, not including the 4 bytes to represent that length.
	//   - dbl.Prologue.Length is the length of the prologue not including unit length, version or prologue length itself.
	//   - So you have UnitLength - PrologueLength - (version_length_bytes(2) + prologue_length_bytes(4)).
	dbl.Instructions = buf.Next(int(dbl.Prologue.UnitLength - dbl.Prologue.Length - 6))

	return dbl
}

func parseDebugLinePrologue(dbl *DebugLineInfo, buf *bytes.Buffer) {
	p := new(DebugLinePrologue)

	p.UnitLength = binary.LittleEndian.Uint32(buf.Next(4))
	p.Version = binary.LittleEndian.Uint16(buf.Next(2))
	if p.Version >= 5 {
		dbl.ptrSize = int(buf.Next(1)[0])  // address_size
		dbl.ptrSize += int(buf.Next(1)[0]) // segment_selector_size
	}

	// Version 4 or earlier
	p.Length = binary.LittleEndian.Uint32(buf.Next(4))
	p.MinInstrLength = uint8(buf.Next(1)[0])
	if p.Version == 4 {
		p.MaxOpPerInstr = uint8(buf.Next(1)[0])
	} else {
		p.MaxOpPerInstr = 1
	}
	p.InitialIsStmt = uint8(buf.Next(1)[0])
	p.LineBase = int8(buf.Next(1)[0])
	p.LineRange = uint8(buf.Next(1)[0])
	p.OpcodeBase = uint8(buf.Next(1)[0])

	p.StdOpLengths = make([]uint8, p.OpcodeBase-1)
	binary.Read(buf, binary.LittleEndian, &p.StdOpLengths)

	dbl.Prologue = p
}

// parseIncludeDirs2 parses the directory table for DWARF version 2 through 4.
func parseIncludeDirs2(info *DebugLineInfo, buf *bytes.Buffer) {
	for {
		str, _ := util.ParseString(buf)
		if str == "" {
			break
		}

		info.IncludeDirs = append(info.IncludeDirs, str)
	}
}

// parseIncludeDirs5 parses the directory table for DWARF version 5.
func parseIncludeDirs5(info *DebugLineInfo, buf *bytes.Buffer) {
	dirEntryFormReader := readEntryFormat(buf, info.Logf)
	dirCount, _ := util.DecodeULEB128(buf)
	info.IncludeDirs = make([]string, 0, dirCount)
	for i := uint64(0); i < dirCount; i++ {
		dirEntryFormReader.reset()
		for dirEntryFormReader.next(buf) {
			switch dirEntryFormReader.contentType {
			case _DW_LNCT_path:
				if dirEntryFormReader.formCode != _DW_FORM_string {
					info.IncludeDirs = append(info.IncludeDirs, dirEntryFormReader.str)
				} else {
					//TODO(aarzilli): support debug_string, debug_line_str
					info.Logf("unsupported string form %#x", dirEntryFormReader.formCode)
				}
			case _DW_LNCT_directory_index:
			case _DW_LNCT_timestamp:
			case _DW_LNCT_size:
			case _DW_LNCT_MD5:
			}
		}
	}
}

// parseFileEntries2 parses the file table for DWARF 2 through 4
func parseFileEntries2(info *DebugLineInfo, buf *bytes.Buffer) {
	for {
		entry := readFileEntry(info, buf, true)
		if entry.Path == "" {
			break
		}

		info.FileNames = append(info.FileNames, entry)
		info.Lookup[entry.Path] = entry
	}
}

func readFileEntry(info *DebugLineInfo, buf *bytes.Buffer, exitOnEmptyPath bool) *FileEntry {
	entry := new(FileEntry)

	entry.Path, _ = util.ParseString(buf)
	if entry.Path == "" && exitOnEmptyPath {
		return entry
	}

	if info.normalizeBackslash {
		entry.Path = strings.Replace(entry.Path, "\\", "/", -1)
	}

	entry.DirIdx, _ = util.DecodeULEB128(buf)
	entry.LastModTime, _ = util.DecodeULEB128(buf)
	entry.Length, _ = util.DecodeULEB128(buf)
	if !filepath.IsAbs(entry.Path) {
		if entry.DirIdx >= 0 && entry.DirIdx < uint64(len(info.IncludeDirs)) {
			entry.Path = filepath.Join(info.IncludeDirs[entry.DirIdx], entry.Path)
		}
	}

	return entry
}

// parseFileEntries5 parses the file table for DWARF 5
func parseFileEntries5(info *DebugLineInfo, buf *bytes.Buffer) {
	fileEntryFormReader := readEntryFormat(buf, info.Logf)
	fileCount, _ := util.DecodeULEB128(buf)
	info.FileNames = make([]*FileEntry, 0, fileCount)
	for i := 0; i < int(fileCount); i++ {
		fileEntryFormReader.reset()
		for fileEntryFormReader.next(buf) {
			entry := new(FileEntry)
			var path string
			var diridx int = -1

			switch fileEntryFormReader.contentType {
			case _DW_LNCT_path:
				if fileEntryFormReader.formCode != _DW_FORM_string {
					path = fileEntryFormReader.str
				} else {
					//TODO(aarzilli): support debug_string, debug_line_str
					info.Logf("unsupported string form %#x", fileEntryFormReader.formCode)
				}
			case _DW_LNCT_directory_index:
				diridx = int(fileEntryFormReader.u64)
			case _DW_LNCT_timestamp:
				entry.LastModTime = fileEntryFormReader.u64
			case _DW_LNCT_size:
				entry.Length = fileEntryFormReader.u64
			case _DW_LNCT_MD5:
				// not implemented
			}

			if diridx >= 0 && !filepath.IsAbs(path) && diridx < len(info.IncludeDirs) {
				path = filepath.Join(info.IncludeDirs[diridx], path)
			}
			entry.Path = path
			info.FileNames = append(info.FileNames, entry)
			info.Lookup[entry.Path] = entry
		}
	}
}
