// elfwriter is a package to write ELF files without having their entire
// contents in memory at any one time.
// This package is incomplete, only features needed to write core files are
// implemented, notably missing:
// - program headers at the beginning of the file
package elfwriter

import (
	"debug/elf"
	"encoding/binary"
	"io"
)

// WriteCloserSeeker is the union of io.Writer, io.Closer and io.Seeker.
type WriteCloserSeeker interface {
	io.Writer
	io.Seeker
	io.Closer
}

// Writer writes ELF files.
type Writer struct {
	w        WriteCloserSeeker
	Err      error
	Progs    []*elf.ProgHeader
	Sections []*elf.SectionHeader

	seekProgHeader int64
	seekProgNum    int64

	seekSectionHeader int64
	seekSectionNum    int64
	seekShstrndx      int64
}

type Note struct {
	Type elf.NType
	Name string
	Data []byte
}

// New creates a new Writer.
func New(w WriteCloserSeeker, fhdr *elf.FileHeader) *Writer {
	const (
		ehsize    = 64
		phentsize = 56
		shentsize = 64
	)

	if seek, _ := w.Seek(0, io.SeekCurrent); seek != 0 {
		panic("can't write halfway through a file")
	}

	r := &Writer{w: w}

	if fhdr.Class != elf.ELFCLASS64 {
		panic("unsupported")
	}

	if fhdr.Data != elf.ELFDATA2LSB {
		panic("unsupported")
	}

	// e_ident
	r.Write([]byte{0x7f, 'E', 'L', 'F', byte(fhdr.Class), byte(fhdr.Data), byte(fhdr.Version), byte(fhdr.OSABI), fhdr.ABIVersion, 0, 0, 0, 0, 0, 0, 0})

	r.u16(uint16(fhdr.Type))    // e_type
	r.u16(uint16(fhdr.Machine)) // e_machine
	r.u32(uint32(fhdr.Version)) // e_version
	r.u64(0)                    // e_entry
	r.seekProgHeader = r.Here()
	r.u64(0) // e_phoff
	r.seekSectionHeader = r.Here()
	r.u64(0)         // e_shoff
	r.u32(0)         // e_flags
	r.u16(ehsize)    // e_ehsize
	r.u16(phentsize) // e_phentsize
	r.seekProgNum = r.Here()
	r.u16(0)         // e_phnum
	r.u16(shentsize) // e_shentsize
	r.seekSectionNum = r.Here()
	r.u16(0) // e_shnum
	r.seekShstrndx = r.Here()
	r.u16(uint16(elf.SHN_UNDEF)) // e_shstrndx

	// Sanity check, size of file header should be the same as ehsize
	if sz, _ := w.Seek(0, io.SeekCurrent); sz != ehsize {
		panic("internal error, ELF header size")
	}

	return r
}

// WriteNotes writes notes to the current location, returns a ProgHeader describing the
// notes.
func (w *Writer) WriteNotes(notes []Note) *elf.ProgHeader {
	if len(notes) == 0 {
		return nil
	}
	h := &elf.ProgHeader{
		Type:  elf.PT_NOTE,
		Align: 4,
	}
	for i := range notes {
		note := &notes[i]
		w.Align(4)
		if h.Off == 0 {
			h.Off = uint64(w.Here())
		}
		w.u32(uint32(len(note.Name)))
		w.u32(uint32(len(note.Data)))
		w.u32(uint32(note.Type))
		w.Write([]byte(note.Name))
		w.Align(4)
		w.Write(note.Data)
	}
	h.Filesz = uint64(w.Here()) - h.Off
	return h
}

// WriteProgramHeaders writes the program headers at the current location
// and patches the file header accordingly.
func (w *Writer) WriteProgramHeaders() {
	phoff := w.Here()

	// Patch File Header
	w.w.Seek(w.seekProgHeader, io.SeekStart)
	w.u64(uint64(phoff))
	w.w.Seek(w.seekProgNum, io.SeekStart)
	w.u16(uint16(len(w.Progs)))
	w.w.Seek(0, io.SeekEnd)

	for _, prog := range w.Progs {
		w.u32(uint32(prog.Type))
		w.u32(uint32(prog.Flags))
		w.u64(prog.Off)
		w.u64(prog.Vaddr)
		w.u64(prog.Paddr)
		w.u64(prog.Filesz)
		w.u64(prog.Memsz)
		w.u64(prog.Align)
	}
}

// WriteSectionHeaders writes the section headers at the current location
// and patches the file header accordingly.
func (w *Writer) WriteSectionHeaders() {
	shstrndx := len(w.Sections)
	strtab := &elf.SectionHeader{
		Name:      ".strtab",
		Type:      elf.SHT_STRTAB,
		Offset:    uint64(w.Here()),
		Addralign: 1,
	}
	w.Sections = append(w.Sections, strtab)

	strtabStart := w.Here()

	strToIndex := map[string]int64{}

	w.Write([]byte{0}) // first entry must be the NUL character

	for _, sect := range w.Sections {
		if _, ok := strToIndex[sect.Name]; ok {
			continue
		}
		strToIndex[sect.Name] = w.Here() - strtabStart
		w.Write([]byte(sect.Name))
		w.Write([]byte{0})
	}

	strtab.Size = uint64(w.Here() - strtabStart)

	shoff := w.Here()

	// Patch File Header
	w.w.Seek(w.seekSectionHeader, io.SeekStart)
	w.u64(uint64(shoff))
	w.w.Seek(w.seekSectionNum, io.SeekStart)
	w.u16(uint16(len(w.Sections)))
	w.w.Seek(w.seekShstrndx, io.SeekStart)
	w.u16(uint16(shstrndx))
	w.w.Seek(0, io.SeekEnd)

	for _, sect := range w.Sections {
		w.u32(uint32(strToIndex[sect.Name]))
		w.u32(uint32(sect.Type))
		w.u64(uint64(sect.Flags))
		w.u64(sect.Addr)
		w.u64(sect.Offset)
		w.u64(sect.Size)
		w.u32(sect.Link)
		w.u32(sect.Info)
		w.u64(sect.Addralign)
		w.u64(sect.Entsize)
	}
}

// Here returns the current seek offset from the start of the file.
func (w *Writer) Here() int64 {
	r, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil && w.Err == nil {
		w.Err = err
	}
	return r
}

// Align writes as many padding bytes as needed to make the current file
// offset a multiple of align.
func (w *Writer) Align(align int64) {
	off := w.Here()
	alignOff := (off + (align - 1)) &^ (align - 1)
	if alignOff-off > 0 {
		w.Write(make([]byte, alignOff-off))
	}
}

func (w *Writer) Write(buf []byte) {
	_, err := w.w.Write(buf)
	if err != nil && w.Err == nil {
		w.Err = err
	}
}

func (w *Writer) u16(n uint16) {
	err := binary.Write(w.w, binary.LittleEndian, n)
	if err != nil && w.Err == nil {
		w.Err = err
	}
}

func (w *Writer) u32(n uint32) {
	err := binary.Write(w.w, binary.LittleEndian, n)
	if err != nil && w.Err == nil {
		w.Err = err
	}
}

func (w *Writer) u64(n uint64) {
	err := binary.Write(w.w, binary.LittleEndian, n)
	if err != nil && w.Err == nil {
		w.Err = err
	}
}
