package frame

import (
	"encoding/binary"
	"io"
	"os"
	"testing"
	"unsafe"
)

func ptrSizeByRuntimeArch() int {
	return int(unsafe.Sizeof(uintptr(0)))
}

func TestFDEForPC(t *testing.T) {
	frames := newFrameIndex()
	frames = append(frames,
		&FrameDescriptionEntry{begin: 10, size: 40},
		&FrameDescriptionEntry{begin: 50, size: 50},
		&FrameDescriptionEntry{begin: 100, size: 100},
		&FrameDescriptionEntry{begin: 300, size: 10})

	for _, test := range []struct {
		pc  uint64
		fde *FrameDescriptionEntry
	}{
		{0, nil},
		{9, nil},
		{10, frames[0]},
		{35, frames[0]},
		{49, frames[0]},
		{50, frames[1]},
		{75, frames[1]},
		{100, frames[2]},
		{199, frames[2]},
		{200, nil},
		{299, nil},
		{300, frames[3]},
		{309, frames[3]},
		{310, nil},
		{400, nil}} {

		out, err := frames.FDEForPC(test.pc)
		if test.fde != nil {
			if err != nil {
				t.Fatal(err)
			}
			if out != test.fde {
				t.Errorf("[pc = %#x] got incorrect fde\noutput:\t%#v\nexpected:\t%#v", test.pc, out, test.fde)
			}
		} else {
			if err == nil {
				t.Errorf("[pc = %#x] expected error got fde %#v", test.pc, out)
			}
		}
	}
}

func TestAppend(t *testing.T) {
	equal := func(x, y FrameDescriptionEntries) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i].Begin() != y[i].Begin() || x[i].End() != y[i].End() {
				return false
			}
		}
		return true
	}
	var appendTests = []struct {
		name string
		f1   FrameDescriptionEntries
		f2   FrameDescriptionEntries
		want FrameDescriptionEntries
	}{
		{
			name: "nil",
			f1:   nil,
			f2:   nil,
			want: nil,
		},

		{
			name: "one",
			f1: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
			},
			f2: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
			},
			want: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
			},
		},
		{
			name: "1 item",
			f1: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 50, size: 50},
			},
			f2: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 50, size: 50},
			},
			want: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 50, size: 50},
			},
		},
		{
			name: "many",
			f1: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 100, size: 100},
				&FrameDescriptionEntry{begin: 50, size: 50},
				&FrameDescriptionEntry{begin: 50, size: 50},
				&FrameDescriptionEntry{begin: 300, size: 10},
				&FrameDescriptionEntry{begin: 300, size: 10},
			},
			f2: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 100, size: 100},
				&FrameDescriptionEntry{begin: 100, size: 100},
			},
			want: FrameDescriptionEntries{
				&FrameDescriptionEntry{begin: 10, size: 40},
				&FrameDescriptionEntry{begin: 50, size: 50},
				&FrameDescriptionEntry{begin: 100, size: 100},
				&FrameDescriptionEntry{begin: 300, size: 10},
			},
		},
	}
	for _, test := range appendTests {
		if got := test.f1.Append(test.f2); !equal(got, test.want) {
			t.Errorf("%v.Append(%v) = %v, want %v", test.f1, test.f2, got, test.want)
		}
	}
}

func BenchmarkFDEForPC(b *testing.B) {
	f, err := os.Open("testdata/frame")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		b.Fatal(err)
	}
	fdes, _ := Parse(data, binary.BigEndian, 0, ptrSizeByRuntimeArch(), 0)

	for i := 0; i < b.N; i++ {
		// bench worst case, exhaustive search
		_, _ = fdes.FDEForPC(0x455555555)
	}
}
