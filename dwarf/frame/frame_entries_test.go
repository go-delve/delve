package frame

import (
	"debug/elf"
	"debug/gosym"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func gosymData(testfile string, t testing.TB) *gosym.Table {
	f, err := os.Open(testfile)
	if err != nil {
		t.Fatal(err)
	}

	e, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}

	return parseGoSym(t, e)
}

func grabDebugFrameSection(fp string, t testing.TB) []byte {
	p, err := filepath.Abs(fp)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}

	ef, err := elf.NewFile(f)
	if err != nil {
		t.Fatal(err)
	}

	data, err := ef.Section(".debug_frame").Data()
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func parseGoSym(t testing.TB, exe *elf.File) *gosym.Table {
	symdat, err := exe.Section(".gosymtab").Data()
	if err != nil {
		t.Fatal(err)
	}

	pclndat, err := exe.Section(".gopclntab").Data()
	if err != nil {
		t.Fatal(err)
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		t.Fatal(err)
	}

	return tab
}

func TestFDEForPC(t *testing.T) {
	fde1 := &FrameDescriptionEntry{AddressRange: &addrange{begin: 100, end: 200}}
	fde2 := &FrameDescriptionEntry{AddressRange: &addrange{begin: 50, end: 99}}
	fde3 := &FrameDescriptionEntry{AddressRange: &addrange{begin: 0, end: 49}}
	fde4 := &FrameDescriptionEntry{AddressRange: &addrange{begin: 201, end: 245}}

	tree := NewFrameIndex()
	tree.Put(fde1)
	tree.Put(fde2)
	tree.Put(fde3)
	tree.Put(fde4)

	node, ok := tree.Find(Addr(35))
	if !ok {
		t.Fatal("Could not find FDE")
	}

	if node != fde3 {
		t.Fatal("Got incorrect fde")
	}
}

func BenchmarkFDEForPC(b *testing.B) {
	f, err := os.Open("testdata/frame")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		b.Fatal(err)
	}
	fdes := Parse(data)

	for i := 0; i < b.N; i++ {
		// bench worst case, exhaustive search
		_, _ = fdes.FDEForPC(0x455555555)
	}
}
