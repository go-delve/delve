package minidump

// Package minidump provides a loader for Windows Minidump files.
// Minidump files are the Windows equivalent of unix core dumps.
// They can be created by the kernel when a program crashes (however this is
// disabled for Go programs) or programmatically using either WinDbg or the
// ProcDump utility.
//
// The file format is described on MSDN starting at:
//  https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_header
// which is the structure found at offset 0 on a minidump file.
//
// Further information on the format can be found reading
// chromium-breakpad's minidump loading code, specifically:
//  https://chromium.googlesource.com/breakpad/breakpad/+/master/src/google_breakpad/common/minidump_cpu_amd64.h
// and:
//  https://chromium.googlesource.com/breakpad/breakpad/+/master/src/google_breakpad/common/minidump_format.h

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"unicode/utf16"
	"unsafe"

	"github.com/derekparker/delve/pkg/proc/winutil"
)

type minidumpBuf struct {
	buf  []byte
	kind string
	off  int
	err  error
	ctx  string
}

func (buf *minidumpBuf) u16() uint16 {
	const stride = 2
	if buf.err != nil {
		return 0
	}
	if buf.off+stride >= len(buf.buf) {
		buf.err = fmt.Errorf("minidump %s truncated at offset %#x while %s", buf.kind, buf.off, buf.ctx)
	}
	r := binary.LittleEndian.Uint16(buf.buf[buf.off : buf.off+stride])
	buf.off += stride
	return r
}

func (buf *minidumpBuf) u32() uint32 {
	const stride = 4
	if buf.err != nil {
		return 0
	}
	if buf.off+stride >= len(buf.buf) {
		buf.err = fmt.Errorf("minidump %s truncated at offset %#x while %s", buf.kind, buf.off, buf.ctx)
	}
	r := binary.LittleEndian.Uint32(buf.buf[buf.off : buf.off+stride])
	buf.off += stride
	return r
}

func (buf *minidumpBuf) u64() uint64 {
	const stride = 8
	if buf.err != nil {
		return 0
	}
	if buf.off+stride >= len(buf.buf) {
		buf.err = fmt.Errorf("minidump %s truncated at offset %#x while %s", buf.kind, buf.off, buf.ctx)
	}
	r := binary.LittleEndian.Uint64(buf.buf[buf.off : buf.off+stride])
	buf.off += stride
	return r
}

func streamBuf(stream *Stream, buf *minidumpBuf, name string) *minidumpBuf {
	return &minidumpBuf{
		buf:  buf.buf,
		kind: "stream",
		off:  stream.Offset,
		err:  nil,
		ctx:  fmt.Sprintf("reading %s stream at %#x", name, stream.Offset),
	}
}

// ErrNotAMinidump is the error returned when the file being loaded is not a
// minidump file.
type ErrNotAMinidump struct {
	what string
	got  uint32
}

func (err ErrNotAMinidump) Error() string {
	return fmt.Sprintf("not a minidump, invalid %s %#x", err.what, err.got)
}

const (
	minidumpSignature = 0x504d444d // 'MDMP'
	minidumpVersion   = 0xa793
)

// Minidump represents a minidump file
type Minidump struct {
	Timestamp uint32
	Flags     FileFlags

	Streams []Stream

	Threads []Thread
	Modules []Module

	Pid uint32

	MemoryRanges []MemoryRange
	MemoryInfo   []MemoryInfo

	streamNum uint32
	streamOff uint32
}

// Stream represents one (uninterpreted) stream in a minidump file.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_directory
type Stream struct {
	Type    StreamType
	Offset  int
	RawData []byte
}

// Thread represents an entry in the ThreadList stream.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_thread
type Thread struct {
	ID            uint32
	SuspendCount  uint32
	PriorityClass uint32
	Priority      uint32
	TEB           uint64
	Context       winutil.CONTEXT
}

// Module represents an entry in the ModuleList stream.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_module
type Module struct {
	BaseOfImage   uint64
	SizeOfImage   uint32
	Checksum      uint32
	TimeDateStamp uint32
	Name          string
	VersionInfo   VSFixedFileInfo

	// CVRecord stores a CodeView record and is populated when a module's debug information resides in a PDB file.  It identifies the PDB file.
	CVRecord []byte

	// MiscRecord is populated when a module's debug information resides in a DBG file.  It identifies the DBG file.  This field is effectively obsolete with modules built by recent toolchains.
	MiscRecord []byte
}

// VSFixedFileInfo: Visual Studio Fixed File Info.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/verrsrc/ns-verrsrc-tagvs_fixedfileinfo
type VSFixedFileInfo struct {
	Signature        uint32
	StructVersion    uint32
	FileVersionHi    uint32
	FileVersionLo    uint32
	ProductVersionHi uint32
	ProductVersionLo uint32
	FileFlagsMask    uint32
	FileFlags        uint32
	FileOS           uint32
	FileType         uint32
	FileSubtype      uint32
	FileDateHi       uint32
	FileDateLo       uint32
}

// MemoryRange represents a region of memory saved to the core file, it's constructed after either:
// 1. parsing an entry in the Memory64List stream.
// 2. parsing the stack field of an entry in the ThreadList stream.
type MemoryRange struct {
	Addr uint64
	Data []byte
}

// ReadMemory reads len(buf) bytes of memory starting at addr into buf from this memory region.
func (m *MemoryRange) ReadMemory(buf []byte, addr uintptr) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if (uint64(addr) < m.Addr) || (uint64(addr)+uint64(len(buf)) > m.Addr+uint64(len(m.Data))) {
		return 0, io.EOF
	}
	copy(buf, m.Data[uint64(addr)-m.Addr:])
	return len(buf), nil
}

// MemoryInfo reprents an entry in the MemoryInfoList stream.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_memory_info
type MemoryInfo struct {
	Addr       uint64
	Size       uint64
	State      MemoryState
	Protection MemoryProtection
	Type       MemoryType
}

//go:generate stringer -type FileFlags,StreamType,Arch,MemoryState,MemoryType,MemoryProtection

// MemoryState is the type of the State field of MINIDUMP_MEMORY_INFO
type MemoryState uint32

const (
	MemoryStateCommit  MemoryState = 0x1000
	MemoryStateReserve MemoryState = 0x2000
	MemoryStateFree    MemoryState = 0x10000
)

// MemoryType is the type of the Type field of MINIDUMP_MEMORY_INFO
type MemoryType uint32

const (
	MemoryTypePrivate MemoryType = 0x20000
	MemoryTypeMapped  MemoryType = 0x40000
	MemoryTypeImage   MemoryType = 0x1000000
)

// MemoryProtection is the type of the Protection field of MINIDUMP_MEMORY_INFO
type MemoryProtection uint32

const (
	MemoryProtectNoAccess         MemoryProtection = 0x01 // PAGE_NOACCESS
	MemoryProtectReadOnly         MemoryProtection = 0x02 // PAGE_READONLY
	MemoryProtectReadWrite        MemoryProtection = 0x04 // PAGE_READWRITE
	MemoryProtectWriteCopy        MemoryProtection = 0x08 // PAGE_WRITECOPY
	MemoryProtectExecute          MemoryProtection = 0x10 // PAGE_EXECUTE
	MemoryProtectExecuteRead      MemoryProtection = 0x20 // PAGE_EXECUTE_READ
	MemoryProtectExecuteReadWrite MemoryProtection = 0x40 // PAGE_EXECUTE_READWRITE
	MemoryProtectExecuteWriteCopy MemoryProtection = 0x80 // PAGE_EXECUTE_WRITECOPY
	// These options can be combined with the previous flags
	MemoryProtectPageGuard    MemoryProtection = 0x100 // PAGE_GUARD
	MemoryProtectNoCache      MemoryProtection = 0x200 // PAGE_NOCACHE
	MemoryProtectWriteCombine MemoryProtection = 0x400 // PAGE_WRITECOMBINE

)

// FileFlags is the type of the Flags field of MINIDUMP_HEADER
type FileFlags uint64

const (
	FileNormal                          FileFlags = 0x00000000
	FileWithDataSegs                    FileFlags = 0x00000001
	FileWithFullMemory                  FileFlags = 0x00000002
	FileWithHandleData                  FileFlags = 0x00000004
	FileFilterMemory                    FileFlags = 0x00000008
	FileScanMemory                      FileFlags = 0x00000010
	FileWithUnloadedModules             FileFlags = 0x00000020
	FileWithIncorrectlyReferencedMemory FileFlags = 0x00000040
	FileFilterModulePaths               FileFlags = 0x00000080
	FileWithProcessThreadData           FileFlags = 0x00000100
	FileWithPrivateReadWriteMemory      FileFlags = 0x00000200
	FileWithoutOptionalData             FileFlags = 0x00000400
	FileWithFullMemoryInfo              FileFlags = 0x00000800
	FileWithThreadInfo                  FileFlags = 0x00001000
	FileWithCodeSegs                    FileFlags = 0x00002000
	FileWithoutAuxilliarySegs           FileFlags = 0x00004000
	FileWithFullAuxilliaryState         FileFlags = 0x00008000
	FileWithPrivateCopyMemory           FileFlags = 0x00010000
	FileIgnoreInaccessibleMemory        FileFlags = 0x00020000
	FileWithTokenInformation            FileFlags = 0x00040000
)

// StreamType is the type of the StreamType field of MINIDUMP_DIRECTORY
type StreamType uint32

const (
	UnusedStream              StreamType = 0
	ReservedStream0           StreamType = 1
	ReservedStream1           StreamType = 2
	ThreadListStream          StreamType = 3
	ModuleListStream          StreamType = 4
	MemoryListStream          StreamType = 5
	ExceptionStream           StreamType = 6
	SystemInfoStream          StreamType = 7
	ThreadExListStream        StreamType = 8
	Memory64ListStream        StreamType = 9
	CommentStreamA            StreamType = 10
	CommentStreamW            StreamType = 11
	HandleDataStream          StreamType = 12
	FunctionTableStream       StreamType = 13
	UnloadedModuleStream      StreamType = 14
	MiscInfoStream            StreamType = 15
	MemoryInfoListStream      StreamType = 16
	ThreadInfoListStream      StreamType = 17
	HandleOperationListStream StreamType = 18
	TokenStream               StreamType = 19
	JavascriptDataStream      StreamType = 20
	SystemMemoryInfoStream    StreamType = 21
	ProcessVMCounterStream    StreamType = 22
)

// Arch is the type of the ProcessorArchitecture field of MINIDUMP_SYSTEM_INFO.
type Arch uint16

const (
	CpuArchitectureX86     Arch = 0
	CpuArchitectureMips    Arch = 1
	CpuArchitectureAlpha   Arch = 2
	CpuArchitecturePPC     Arch = 3
	CpuArchitectureSHX     Arch = 4 // Super-H
	CpuArchitectureARM     Arch = 5
	CpuArchitectureIA64    Arch = 6
	CpuArchitectureAlpha64 Arch = 7
	CpuArchitectureMSIL    Arch = 8 // Microsoft Intermediate Language
	CpuArchitectureAMD64   Arch = 9
	CpuArchitectureWoW64   Arch = 10
	CpuArchitectureARM64   Arch = 12
	CpuArchitectureUnknown Arch = 0xffff
)

// Open reads the minidump file at path and returns it as a Minidump structure.
func Open(path string, logfn func(fmt string, args ...interface{})) (*Minidump, error) {
	rawbuf, err := ioutil.ReadFile(path) //TODO(aarzilli): mmap?
	if err != nil {
		return nil, err
	}

	buf := &minidumpBuf{buf: rawbuf, kind: "file"}

	var mdmp Minidump

	readMinidumpHeader(&mdmp, buf)
	if buf.err != nil {
		return nil, buf.err
	}

	if logfn != nil {
		logfn("Minidump Header\n")
		logfn("Num Streams: %d\n", mdmp.streamNum)
		logfn("Streams offset: %#x\n", mdmp.streamOff)
		logfn("File flags: %s\n", fileFlagsToString(mdmp.Flags))
		logfn("Offset after header %#x\n", buf.off)
	}

	readDirectory(&mdmp, buf)
	if buf.err != nil {
		return nil, buf.err
	}

	for i := range mdmp.Streams {
		stream := &mdmp.Streams[i]
		if stream.Type != SystemInfoStream {
			continue
		}

		sb := streamBuf(stream, buf, "system info")
		if buf.err != nil {
			return nil, buf.err
		}

		arch := Arch(sb.u16())

		if logfn != nil {
			logfn("Found processor architecture %s\n", arch.String())
		}

		if arch != CpuArchitectureAMD64 {
			return nil, fmt.Errorf("unsupported architecture %s", arch.String())
		}
	}

	for i := range mdmp.Streams {
		stream := &mdmp.Streams[i]
		if logfn != nil {
			logfn("Stream %d: type:%s off:%#x size:%#x\n", i, stream.Type, stream.Offset, len(stream.RawData))
		}
		switch stream.Type {
		case ThreadListStream:
			readThreadList(&mdmp, streamBuf(stream, buf, "thread list"))
			if logfn != nil {
				for i := range mdmp.Threads {
					logfn("\tID:%#x TEB:%#x\n", mdmp.Threads[i].ID, mdmp.Threads[i].TEB)
				}
			}
		case ModuleListStream:
			readModuleList(&mdmp, streamBuf(stream, buf, "module list"))
			if logfn != nil {
				for i := range mdmp.Modules {
					logfn("\tName:%q BaseOfImage:%#x SizeOfImage:%#x\n", mdmp.Modules[i].Name, mdmp.Modules[i].BaseOfImage, mdmp.Modules[i].SizeOfImage)
				}
			}
		case ExceptionStream:
			//TODO(aarzilli): this stream contains the exception that made the
			//process stop and caused the minidump to be taken. If we ever start
			//caring about this we should parse this.
		case Memory64ListStream:
			readMemory64List(&mdmp, streamBuf(stream, buf, "memory64 list"), logfn)
		case MemoryInfoListStream:
			readMemoryInfoList(&mdmp, streamBuf(stream, buf, "memory info list"), logfn)
		case MiscInfoStream:
			readMiscInfo(&mdmp, streamBuf(stream, buf, "misc info"))
			if logfn != nil {
				logfn("\tPid: %#x\n", mdmp.Pid)
			}
		case CommentStreamW:
			if logfn != nil {
				logfn("\t%q\n", decodeUTF16(stream.RawData))
			}
		case CommentStreamA:
			if logfn != nil {
				logfn("\t%s\n", string(stream.RawData))
			}
		}
		if buf.err != nil {
			return nil, buf.err
		}
	}

	return &mdmp, nil
}

// decodeUTF16 converts a NUL-terminated UTF16LE string to (non NUL-terminated) UTF8.
func decodeUTF16(in []byte) string {
	utf16encoded := []uint16{}
	for i := 0; i+1 < len(in); i += 2 {
		var ch uint16
		ch = uint16(in[i]) + uint16(in[i+1])<<8
		utf16encoded = append(utf16encoded, ch)
	}
	s := string(utf16.Decode(utf16encoded))
	if len(s) > 0 && s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return s
}

func fileFlagsToString(flags FileFlags) string {
	out := []byte{}
	for i, name := range _FileFlags_map {
		if i == 0 {
			continue
		}
		if flags&i != 0 {
			if len(out) > 0 {
				out = append(out, '|')
			}
			out = append(out, name...)
		}
	}
	if len(out) == 0 {
		return flags.String()
	}
	return string(out)
}

// readMinidumpHeader reads the minidump file header
func readMinidumpHeader(mdmp *Minidump, buf *minidumpBuf) {
	buf.ctx = "reading minidump header"

	if sig := buf.u32(); sig != minidumpSignature {
		buf.err = ErrNotAMinidump{"signature", sig}
		return
	}

	if ver := buf.u16(); ver != minidumpVersion {
		buf.err = ErrNotAMinidump{"version", uint32(ver)}
		return
	}

	buf.u16() // implementation specific version
	mdmp.streamNum = buf.u32()
	mdmp.streamOff = buf.u32()
	buf.u32() // checksum, but it's always 0
	mdmp.Timestamp = buf.u32()
	mdmp.Flags = FileFlags(buf.u64())
}

// readDirectory reads the list of streams (i.e. the minidum "directory")
func readDirectory(mdmp *Minidump, buf *minidumpBuf) {
	buf.off = int(mdmp.streamOff)

	mdmp.Streams = make([]Stream, mdmp.streamNum)
	for i := range mdmp.Streams {
		buf.ctx = fmt.Sprintf("reading stream directory entry %d", i)
		stream := &mdmp.Streams[i]
		stream.Type = StreamType(buf.u32())
		stream.Offset, stream.RawData = readLocationDescriptor(buf)
		if buf.err != nil {
			return
		}
	}
}

// readLocationDescriptor reads a location descriptor structure (a structure
// which describes a subregion of the file), and returns the destination
// offset and a slice into the minidump file's buffer.
func readLocationDescriptor(buf *minidumpBuf) (off int, rawData []byte) {
	sz := buf.u32()
	off = int(buf.u32())
	if buf.err != nil {
		return off, nil
	}
	end := off + int(sz)
	if off >= len(buf.buf) || end > len(buf.buf) {
		buf.err = fmt.Errorf("location starting at %#x of size %#x is past the end of file, while %s", off, sz, buf.ctx)
		return 0, nil
	}
	rawData = buf.buf[off:end]
	return
}

func readString(buf *minidumpBuf) string {
	startOff := buf.off
	sz := buf.u32()
	if buf.err != nil {
		return ""
	}
	end := buf.off + int(sz)
	if buf.off >= len(buf.buf) || end > len(buf.buf) {
		buf.err = fmt.Errorf("string starting at %#x of size %#x is past the end of file, while %s", startOff, sz, buf.ctx)
		return ""
	}
	return decodeUTF16(buf.buf[buf.off:end])
}

// readThreadList reads a thread list stream and adds the threads to the minidump.
func readThreadList(mdmp *Minidump, buf *minidumpBuf) {
	threadNum := buf.u32()
	if buf.err != nil {
		return
	}

	mdmp.Threads = make([]Thread, threadNum)

	for i := range mdmp.Threads {
		buf.ctx = fmt.Sprintf("reading thread list entry %d", i)
		thread := &mdmp.Threads[i]

		thread.ID = buf.u32()
		thread.SuspendCount = buf.u32()
		thread.PriorityClass = buf.u32()
		thread.Priority = buf.u32()
		thread.TEB = buf.u64()
		if buf.err != nil {
			return
		}

		readMemoryDescriptor(mdmp, buf)                    // thread stack
		_, rawThreadContext := readLocationDescriptor(buf) // thread context
		thread.Context = *((*winutil.CONTEXT)(unsafe.Pointer(&rawThreadContext[0])))
		if buf.err != nil {
			return
		}
	}
}

// readModuleList reads a module list stream and adds the modules to the minidump.
func readModuleList(mdmp *Minidump, buf *minidumpBuf) {
	moduleNum := buf.u32()
	if buf.err != nil {
		return
	}

	mdmp.Modules = make([]Module, moduleNum)

	for i := range mdmp.Modules {
		buf.ctx = fmt.Sprintf("reading module list entry %d", i)
		module := &mdmp.Modules[i]

		module.BaseOfImage = buf.u64()
		module.SizeOfImage = buf.u32()
		module.Checksum = buf.u32()
		module.TimeDateStamp = buf.u32()
		nameOff := int(buf.u32())

		versionInfoVec := make([]uint32, unsafe.Sizeof(VSFixedFileInfo{})/unsafe.Sizeof(uint32(0)))
		for j := range versionInfoVec {
			versionInfoVec[j] = buf.u32()
		}

		module.VersionInfo = *(*VSFixedFileInfo)(unsafe.Pointer(&versionInfoVec[0]))

		_, module.CVRecord = readLocationDescriptor(buf)
		_, module.MiscRecord = readLocationDescriptor(buf)

		if buf.err != nil {
			return
		}

		nameBuf := minidumpBuf{buf: buf.buf, kind: "file", off: nameOff, err: nil, ctx: buf.ctx}
		module.Name = readString(&nameBuf)
		if nameBuf.err != nil {
			buf.err = nameBuf.err
			return
		}
	}
}

// readMemory64List reads a _MINIDUMP_MEMORY64_LIST structure, containing
// the description of the process memory.
// See: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_memory64_list
// And: https://docs.microsoft.com/en-us/windows/desktop/api/minidumpapiset/ns-minidumpapiset-_minidump_memory_descriptor
func readMemory64List(mdmp *Minidump, buf *minidumpBuf, logfn func(fmt string, args ...interface{})) {
	rangesNum := buf.u64()
	baseOff := int(buf.u64())
	if buf.err != nil {
		return
	}

	for i := uint64(0); i < rangesNum; i++ {
		addr := buf.u64()
		sz := buf.u64()

		end := baseOff + int(sz)
		if baseOff >= len(buf.buf) || end > len(buf.buf) {
			buf.err = fmt.Errorf("memory range at %#x of size %#x is past the end of file, while %s", baseOff, sz, buf.ctx)
			return
		}

		mdmp.addMemory(addr, buf.buf[baseOff:end])

		if logfn != nil {
			logfn("\tMemory %d addr:%#x size:%#x FileOffset:%#x\n", i, addr, sz, baseOff)
		}

		baseOff = end
	}
}

func readMemoryInfoList(mdmp *Minidump, buf *minidumpBuf, logfn func(fmt string, args ...interface{})) {
	startOff := buf.off
	sizeOfHeader := int(buf.u32())
	sizeOfEntry := int(buf.u32())
	numEntries := buf.u64()

	buf.off = startOff + sizeOfHeader

	mdmp.MemoryInfo = make([]MemoryInfo, numEntries)

	for i := range mdmp.MemoryInfo {
		memInfo := &mdmp.MemoryInfo[i]
		startOff := buf.off

		memInfo.Addr = buf.u64()
		buf.u64() // allocation_base

		buf.u32() // allocation_protection
		buf.u32() // alignment

		memInfo.Size = buf.u64()

		memInfo.State = MemoryState(buf.u32())
		memInfo.Protection = MemoryProtection(buf.u32())
		memInfo.Type = MemoryType(buf.u32())

		if logfn != nil {
			logfn("\tMemoryInfo %d Addr:%#x Size:%#x %s %s %s\n", i, memInfo.Addr, memInfo.Size, memInfo.State, memInfo.Protection, memInfo.Type)
		}

		buf.off = startOff + sizeOfEntry
	}
}

// readMiscInfo reads the process_id from a MiscInfo stream.
func readMiscInfo(mdmp *Minidump, buf *minidumpBuf) {
	buf.u32() // size of info
	buf.u32() // flags1

	mdmp.Pid = buf.u32() // process_id
	// there are more fields here, but we don't care about them
}

// readMemoryDescriptor reads a memory descriptor struct and adds it to the memory map of the minidump.
func readMemoryDescriptor(mdmp *Minidump, buf *minidumpBuf) {
	addr := buf.u64()
	if buf.err != nil {
		return
	}
	_, rawData := readLocationDescriptor(buf)
	if buf.err != nil {
		return
	}
	mdmp.addMemory(addr, rawData)
}

func (mdmp *Minidump) addMemory(addr uint64, data []byte) {
	mdmp.MemoryRanges = append(mdmp.MemoryRanges, MemoryRange{addr, data})
}
