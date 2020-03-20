package godwarf

import (
	"testing"
)

func makeRanges(v ...uint64) [][2]uint64 {
	r := make([][2]uint64, 0, len(v)/2)
	for i := 0; i < len(v); i += 2 {
		r = append(r, [2]uint64{v[i], v[i+1]})
	}
	return r
}

func assertRanges(t *testing.T, out, tgt [][2]uint64) {
	if len(out) != len(tgt) {
		t.Errorf("\nexpected:\t%v\ngot:\t\t%v", tgt, out)
	}
	for i := range out {
		if out[i] != tgt[i] {
			t.Errorf("\nexpected:\t%v\ngot:\t\t%v", tgt, out)
			break
		}
	}
}

func TestNormalizeRanges(t *testing.T) {
	mr := makeRanges
	//assertRanges(t, normalizeRanges(mr(105, 103, 90, 95, 25, 20, 20, 23)), mr(20, 23, 90, 95))
	assertRanges(t, normalizeRanges(mr(10, 12, 12, 15)), mr(10, 15))
	assertRanges(t, normalizeRanges(mr(12, 15, 10, 12)), mr(10, 15))
	assertRanges(t, normalizeRanges(mr(4910012, 4910013, 4910013, 4910098, 4910124, 4910127)), mr(4910012, 4910098, 4910124, 4910127))
}

func TestRangeContains(t *testing.T) {
	mr := func(start, end uint64) [2]uint64 {
		return [2]uint64{start, end}
	}
	tcs := []struct {
		a, b [2]uint64
		tgt  bool
	}{
		{mr(1, 10), mr(1, 11), false},
		{mr(1, 10), mr(1, 1), true},
		{mr(1, 10), mr(10, 11), false},
		{mr(1, 10), mr(1, 10), true},
		{mr(1, 10), mr(2, 5), true},
	}

	for _, tc := range tcs {
		if rangeContains(tc.a, tc.b) != tc.tgt {
			if tc.tgt {
				t.Errorf("range %v does not contan %v (but should)", tc.a, tc.b)
			} else {
				t.Errorf("range %v does contain %v (but shouldn't)", tc.a, tc.b)
			}
		}
	}
}

func TestRangesContains(t *testing.T) {
	mr := makeRanges
	tcs := []struct {
		rngs1, rngs2 [][2]uint64
		tgt          bool
	}{
		{mr(1, 10), mr(1, 11), false},
		{mr(1, 10), mr(1, 1), true},
		{mr(1, 10), mr(10, 11), false},
		{mr(1, 10), mr(1, 10), true},
		{mr(1, 10), mr(2, 5), true},

		{mr(1, 10, 20, 30), mr(1, 11), false},
		{mr(1, 10, 20, 30), mr(1, 1, 20, 22), true},
		{mr(1, 10, 20, 30), mr(30, 31), false},
		{mr(1, 10, 20, 30), mr(15, 17), false},
		{mr(1, 10, 20, 30), mr(1, 5, 6, 9, 21, 24), true},
		{mr(1, 10, 20, 30), mr(0, 1), false},
	}

	for _, tc := range tcs {
		if rangesContains(tc.rngs1, tc.rngs2) != tc.tgt {
			if tc.tgt {
				t.Errorf("ranges %v does not contan %v (but should)", tc.rngs1, tc.rngs2)
			} else {
				t.Errorf("ranges %v does contain %v (but shouldn't)", tc.rngs1, tc.rngs2)
			}
		}
	}
}

func TestContainsPC(t *testing.T) {
	mr := makeRanges

	tcs := []struct {
		rngs [][2]uint64
		pc   uint64
		tgt  bool
	}{
		{mr(1, 10), 1, true},
		{mr(1, 10), 5, true},
		{mr(1, 10), 10, false},
		{mr(1, 10, 20, 30), 15, false},
		{mr(1, 10, 20, 30), 20, true},
		{mr(1, 10, 20, 30), 30, false},
		{mr(1, 10, 20, 30), 31, false},
	}

	for _, tc := range tcs {
		n := &Tree{Ranges: tc.rngs}
		if n.ContainsPC(tc.pc) != tc.tgt {
			if tc.tgt {
				t.Errorf("ranges %v does not contain %d (but should)", tc.rngs, tc.pc)
			} else {
				t.Errorf("ranges %v does contain %d (but shouldn't)", tc.rngs, tc.pc)
			}
		}
	}
}
