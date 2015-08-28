package line

import (
	"bytes"
	"encoding/binary"

	"github.com/derekparker/delve/dwarf/util"
)

type DebugLinePrologue struct {
	UnitLength     uint32
	Version        uint16
	Length         uint32
	MinInstrLength uint8
	InitialIsStmt  uint8
	LineBase       int8
	LineRange      uint8
	OpcodeBase     uint8
	StdOpLengths   []uint8
}

type DebugLineInfo struct {
	Prologue     *DebugLinePrologue
	IncludeDirs  []string
	FileNames    []*FileEntry
	Instructions []byte
	Lookup       map[string]*FileEntry
}

type FileEntry struct {
	Name        string
	DirIdx      uint64
	LastModTime uint64
	Length      uint64
}

type DebugLines []*DebugLineInfo

func (d *DebugLines) GetLineInfo(name string) *DebugLineInfo {
	/*
		Find in which table file exists.
		Return that table
	*/
	for _, l := range *d {
		if fe := l.GetFileEntry(name); fe != nil {
			return l
		}
	}

	return nil
}

func (d *DebugLineInfo) GetFileEntry(name string) *FileEntry {
	return d.Lookup[name]
}

func Parse(data []byte) DebugLines {
	var (
		lines = make(DebugLines, 0)
		buf   = bytes.NewBuffer(data)
	)

	// we have to scan multiple file name tables here
	for buf.Len() > 0 {
		dbl := new(DebugLineInfo)
		dbl.Lookup = make(map[string]*FileEntry)

		parseDebugLinePrologue(dbl, buf)
		parseIncludeDirs(dbl, buf)
		parseFileEntries(dbl, buf)

		/*
			Instructions size calculation breakdown:

			- dbl.Prologue.UnitLength is the length of the entire unit, not including the 4 bytes to represent that length.
			- dbl.Prologue.Length is the length of the prologue not including unit length, version or the prologue length itself.
			- So you have UnitLength - PrologueLength - (version_length_bytes(2) + prologue_length_bytes(4)).
			- thanks to Derek for ^
		*/
		dbl.Instructions = buf.Next(int(dbl.Prologue.UnitLength - dbl.Prologue.Length - 6))

		lines = append(lines, dbl)
	}

	return lines
}

func parseDebugLinePrologue(dbl *DebugLineInfo, buf *bytes.Buffer) {
	p := new(DebugLinePrologue)

	p.UnitLength = binary.LittleEndian.Uint32(buf.Next(4))
	p.Version = binary.LittleEndian.Uint16(buf.Next(2))
	p.Length = binary.LittleEndian.Uint32(buf.Next(4))
	p.MinInstrLength = uint8(buf.Next(1)[0])
	p.InitialIsStmt = uint8(buf.Next(1)[0])
	p.LineBase = int8(buf.Next(1)[0])
	p.LineRange = uint8(buf.Next(1)[0])
	p.OpcodeBase = uint8(buf.Next(1)[0])

	p.StdOpLengths = make([]uint8, p.OpcodeBase-1)
	binary.Read(buf, binary.LittleEndian, &p.StdOpLengths)

	dbl.Prologue = p
}

func parseIncludeDirs(info *DebugLineInfo, buf *bytes.Buffer) {
	for {
		str, _ := util.ParseString(buf)
		if str == "" {
			break
		}

		info.IncludeDirs = append(info.IncludeDirs, str)
	}
}

func parseFileEntries(info *DebugLineInfo, buf *bytes.Buffer) {
	for {
		entry := new(FileEntry)

		name, _ := util.ParseString(buf)
		if name == "" {
			break
		}

		entry.Name = name
		entry.DirIdx, _ = util.DecodeULEB128(buf)
		entry.LastModTime, _ = util.DecodeULEB128(buf)
		entry.Length, _ = util.DecodeULEB128(buf)

		info.FileNames = append(info.FileNames, entry)
		info.Lookup[name] = entry
	}
}
