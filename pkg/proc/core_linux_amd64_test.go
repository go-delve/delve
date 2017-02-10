package proc

import (
	"bytes"
	"io/ioutil"
	"os/exec"
	"reflect"
	"testing"

	"fmt"

	"path"

	"github.com/derekparker/delve/pkg/proc/test"
)

func TestSplicedReader(t *testing.T) {
	data := []byte{}
	data2 := []byte{}
	for i := 0; i < 100; i++ {
		data = append(data, byte(i))
		data2 = append(data2, byte(i+100))
	}

	type region struct {
		data   []byte
		off    uintptr
		length uintptr
	}
	tests := []struct {
		name     string
		regions  []region
		readAddr uintptr
		readLen  int
		want     []byte
	}{
		{
			"Insert after",
			[]region{
				{data, 0, 1},
				{data2, 1, 1},
			},
			0,
			2,
			[]byte{0, 101},
		},
		{
			"Insert before",
			[]region{
				{data, 1, 1},
				{data2, 0, 1},
			},
			0,
			2,
			[]byte{100, 1},
		},
		{
			"Completely overwrite",
			[]region{
				{data, 1, 1},
				{data2, 0, 3},
			},
			0,
			3,
			[]byte{100, 101, 102},
		},
		{
			"Overwrite end",
			[]region{
				{data, 0, 2},
				{data2, 1, 2},
			},
			0,
			3,
			[]byte{0, 101, 102},
		},
		{
			"Overwrite start",
			[]region{
				{data, 0, 3},
				{data2, 0, 2},
			},
			0,
			3,
			[]byte{100, 101, 2},
		},
		{
			"Punch hole",
			[]region{
				{data, 0, 5},
				{data2, 1, 3},
			},
			0,
			5,
			[]byte{0, 101, 102, 103, 4},
		},
		{
			"Overlap two",
			[]region{
				{data, 10, 4},
				{data, 14, 4},
				{data2, 12, 4},
			},
			10,
			8,
			[]byte{10, 11, 112, 113, 114, 115, 16, 17},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mem := &SplicedMemory{}
			for _, region := range test.regions {
				r := bytes.NewReader(region.data)
				mem.Add(&OffsetReaderAt{r, 0}, region.off, region.length)
			}
			got := make([]byte, test.readLen)
			n, err := mem.ReadMemory(got, test.readAddr)
			if n != test.readLen || err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("ReadAt = %v, %v, %v, want %v, %v, %v", n, err, got, test.readLen, nil, test.want)
			}
		})
	}
}

func TestReadCore(t *testing.T) {
	// This is all very fragile and won't work on hosts with non-default core patterns.
	// Might be better to check in the core?
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	fix := test.BuildFixture("panic")
	bashCmd := fmt.Sprintf("cd %v && ulimit -c unlimited && GOTRACEBACK=crash %v", tempDir, fix.Path)
	exec.Command("bash", "-c", bashCmd).Run()
	corePath := path.Join(tempDir, "core")

	core, err := readCore(corePath, fix.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(core.Threads) == 0 {
		t.Error("expected at least one thread")
	}
	// Would be good to test more stuff but not sure what without reading debug information, etc.
}
