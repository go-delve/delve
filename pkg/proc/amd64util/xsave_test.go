package amd64util

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestAMD64XstateReadHi16ZMMIndependent(t *testing.T) {
	// Any register from ZMM16 through ZMM31 can contain data while ZMM0 through
	// ZMM15 are zero. XSAVE may therefore set bit 7 while leaving bits 2 and 6
	// clear. See Intel SDM
	// Volume 1, revision 081, sections 13.5.5, and 13.7:
	// https://cdrdv2-public.intel.com/789574/253665-sdm-vol-1.pdf
	area := make([]byte, 2688)
	binary.LittleEndian.PutUint64(area[_XSAVE_HEADER_START:], 1<<7) // Only Hi16_ZMM is present.
	want := bytes.Repeat([]byte{42}, 64)
	copy(area[1664+4*64:], want) // ZMM20 in the standard Intel XSAVE layout.

	var state AMD64Xstate
	if err := AMD64XstateRead(area, false, &state, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, reg := range state.Decode() {
		if reg.Name == "XMM20" {
			if !bytes.Equal(reg.Reg.Bytes, want) {
				t.Fatalf("XMM20 = %x, want %x", reg.Reg.Bytes, want)
			}
			return
		}
	}
	t.Fatal("XMM20 not found in decoded registers")
}
