package line

import (
	"bytes"
	"encoding/binary"

	"github.com/derekparker/dbg/dwarf/util"
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
		dbl = &DebugLineInfo{}
		buf = bytes.NewBuffer(data)
	)

	parseDebugLinePrologue(dbl, buf)
	parseIncludeDirs(dbl, buf)
	parseFileEntries(dbl, buf)
	dbl.Instructions = buf.Bytes()

	return dbl
}

func parseDebugLinePrologue(dbl *DebugLineInfo, buf *bytes.Buffer) {
	p := &DebugLinePrologue{}

	binary.Read(buf, binary.LittleEndian, &p.Length)
	binary.Read(buf, binary.LittleEndian, &p.Version)
	binary.Read(buf, binary.LittleEndian, &p.PrologueLength)
	binary.Read(buf, binary.LittleEndian, &p.MinInstrLength)
	binary.Read(buf, binary.LittleEndian, &p.InitialIsStmt)
	binary.Read(buf, binary.LittleEndian, &p.LineBase)
	binary.Read(buf, binary.LittleEndian, &p.LineRange)
	binary.Read(buf, binary.LittleEndian, &p.OpcodeBase)
	binary.Read(buf, binary.LittleEndian, &p.StdOpLengths)

	p.StdOpLengths = make([]uint8, p.OpcodeBase-1)
	for i := uint8(0); i < p.OpcodeBase-1; i++ {
		binary.Read(buf, binary.LittleEndian, &p.StdOpLengths[i])
	}

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
		entry := FileEntry{}

		name, _ := util.ParseString(buf)
		if name == "" {
			break
		}

		entry.Name = name
		entry.DirIdx, _ = util.DecodeULEB128(buf)
		entry.LastModTime, _ = util.DecodeULEB128(buf)
		entry.Length, _ = util.DecodeULEB128(buf)

		info.FileNames = append(info.FileNames, &entry)
	}
}
