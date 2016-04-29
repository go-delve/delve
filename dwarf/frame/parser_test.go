package frame_test

import (
	"encoding/binary"
	"io/ioutil"
	"os"
	"testing"

	"github.com/davecheney/profile"
	"github.com/derekparker/delve/dwarf/frame"
)

func BenchmarkParse(b *testing.B) {
	defer profile.Start(profile.CPUProfile).Stop()
	f, err := os.Open("testdata/frame")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Parse(data, binary.BigEndian)
	}
}
