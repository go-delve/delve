package amd64util

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
)

func TestAMD64XstateReadHi16ZMM(t *testing.T) {
	const offset = 704
	xsave := make([]byte, offset+16*64)
	binary.LittleEndian.PutUint64(xsave[_XSAVE_HEADER_START:], 1<<7)
	want := fillHi16ZMM(xsave[offset:])

	var xstate AMD64Xstate
	if err := AMD64XstateRead(xsave, false, &xstate, 0, offset); err != nil {
		t.Fatal(err)
	}

	regs := xstate.Decode()
	for i := range 16 {
		name := fmt.Sprintf("XMM%d", i+16)
		got := registerBytes(regs, name)
		if !bytes.Equal(got, want[i]) {
			t.Errorf("%s = %x, want %x", name, got, want[i])
		}
	}
}

func TestAMD64XstateReadHi16ZMMAbsent(t *testing.T) {
	const offset = 704
	xsave := make([]byte, offset+16*64)
	fillHi16ZMM(xsave[offset:])

	var xstate AMD64Xstate
	if err := AMD64XstateRead(xsave, false, &xstate, 0, offset); err != nil {
		t.Fatal(err)
	}
	if got := registerBytes(xstate.Decode(), "XMM16"); got != nil {
		t.Errorf("XMM16 = %x, want absent", got)
	}
}

func TestAMD64XstateReadHi16ZMMTruncated(t *testing.T) {
	const offset = 704
	xsave := make([]byte, offset+16*64-1)
	binary.LittleEndian.PutUint64(xsave[_XSAVE_HEADER_START:], 1<<7)

	var xstate AMD64Xstate
	if err := AMD64XstateRead(xsave, false, &xstate, 0, offset); err == nil {
		t.Fatal("AMD64XstateRead returned nil error for truncated Hi16_ZMM state")
	}
}

func TestAMD64XstateReadHi16ZMMGuessesCoreOffset(t *testing.T) {
	tests := []struct {
		name   string
		size   int
		offset int
		pkru   bool
	}{
		{name: "Intel", size: 2688, offset: 1664},
		{name: "AMD", size: 2440, offset: 1408, pkru: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xsave := make([]byte, tc.size)
			binary.LittleEndian.PutUint64(xsave[_XSAVE_HEADER_START:], 1<<7)
			if tc.pkru {
				binary.LittleEndian.PutUint64(xsave[_I386_LINUX_XSAVE_XCR0_OFFSET:], 1<<9)
			}
			want := fillHi16ZMM(xsave[tc.offset:])

			var xstate AMD64Xstate
			if err := AMD64XstateRead(xsave, false, &xstate, 0, 0); err != nil {
				t.Fatal(err)
			}
			got16 := registerBytes(xstate.Decode(), "XMM16")
			if !bytes.Equal(got16, want[0]) {
				t.Errorf("XMM16 = %x, want %x", got16, want[0])
			}
			got31 := registerBytes(xstate.Decode(), "XMM31")
			if !bytes.Equal(got31, want[15]) {
				t.Errorf("XMM31 = %x, want %x", got31, want[15])
			}
		})
	}
}

func fillHi16ZMM(dst []byte) [16][]byte {
	var values [16][]byte
	for i := range values {
		values[i] = make([]byte, 64)
		for j := range values[i] {
			values[i][j] = byte(i + j)
		}
		copy(dst[i*64:], values[i])
	}
	return values
}

func registerBytes(regs []proc.Register, name string) []byte {
	for _, reg := range regs {
		if reg.Name == name {
			return reg.Reg.Bytes
		}
	}
	return nil
}
