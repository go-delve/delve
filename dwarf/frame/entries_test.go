package frame

import (
	"io/ioutil"
	"os"
	"testing"
)

func TestFDEForPC(t *testing.T) {
	fde1 := &FrameDescriptionEntry{begin: 0, end: 49}
	fde2 := &FrameDescriptionEntry{begin: 50, end: 99}
	fde3 := &FrameDescriptionEntry{begin: 100, end: 200}
	fde4 := &FrameDescriptionEntry{begin: 201, end: 245}

	frames := NewFrameIndex()
	frames = append(frames, fde1)
	frames = append(frames, fde2)
	frames = append(frames, fde3)
	frames = append(frames, fde4)

	node, err := frames.FDEForPC(35)
	if err != nil {
		t.Fatal("Could not find FDE")
	}

	if node != fde1 {
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
