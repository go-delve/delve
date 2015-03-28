package line

import (
	"bytes"
	"encoding/binary"

	"github.com/derekparker/delve/dwarf/util"
)

type DebugLinePrologue struct {
	Length         uint32
	Version        uint16
	PrologueLength uint32
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
}

type FileEntry struct {
	Name        string
	DirIdx      uint64
	LastModTime uint64
	Length      uint64
}

func Parse(data []byte) *DebugLineInfo {
	var (
		dbl = new(DebugLineInfo)
		buf = bytes.NewBuffer(data)
	)

	parseDebugLinePrologue(dbl, buf)
	parseIncludeDirs(dbl, buf)
	parseFileEntries(dbl, buf)
	dbl.Instructions = buf.Bytes()

	return dbl
}

func parseDebugLinePrologue(dbl *DebugLineInfo, buf *bytes.Buffer) {
	p := new(DebugLinePrologue)

	p.Length = binary.LittleEndian.Uint32(buf.Next(4))
	p.Version = binary.LittleEndian.Uint16(buf.Next(2))
	p.PrologueLength = binary.LittleEndian.Uint32(buf.Next(4))
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
	}
}
