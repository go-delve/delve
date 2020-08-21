package cmds

import (
	"testing"
)

func TestParseRedirects(t *testing.T) {
	testCases := []struct {
		in     []string
		tgt    [3]string
		tgterr string
	}{
		{
			[]string{"one.txt"},
			[3]string{"one.txt", "", ""},
			"",
		},
		{
			[]string{"one.txt", "two.txt"},
			[3]string{},
			"redirect error: stdin redirected twice",
		},
		{
			[]string{"stdout:one.txt"},
			[3]string{"", "one.txt", ""},
			"",
		},
		{
			[]string{"stdout:one.txt", "stderr:two.txt", "stdin:three.txt"},
			[3]string{"three.txt", "one.txt", "two.txt"},
			"",
		},
		{
			[]string{"stdout:one.txt", "stderr:two.txt", "three.txt"},
			[3]string{"three.txt", "one.txt", "two.txt"},
			"",
		},
	}

	for _, tc := range testCases {
		t.Logf("input: %q", tc.in)
		out, err := parseRedirects(tc.in)
		t.Logf("output: %q error %v", out, err)
		if tc.tgterr != "" {
			if err == nil {
				t.Errorf("Expected error %q, got output %q", tc.tgterr, out)
			} else if errstr := err.Error(); errstr != tc.tgterr {
				t.Errorf("Expected error %q, got error %q", tc.tgterr, errstr)
			}
		} else {
			for i := range tc.tgt {
				if tc.tgt[i] != out[i] {
					t.Errorf("Expected %q, got %q (mismatch at index %d)", tc.tgt, out, i)
					break
				}
			}
		}
	}
}
