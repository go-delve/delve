package gobuild

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/go-delve/delve/pkg/config"
)

func TestGoBuildArgsDashC(t *testing.T) {
	testCases := []struct {
		in, tgt string
		isTest  bool
	}{
		{"-C somedir", "-C somedir -o debug -gcflags 'all=-N -l' pkg", false},
		{"-C", "-o debug -gcflags 'all=-N -l' -C pkg", false},
		{"-C=somedir", "-C=somedir -o debug -gcflags 'all=-N -l' pkg", false},
		{"-C somedir -other -args", "-C somedir -o debug -gcflags 'all=-N -l' -other -args pkg", false},
		{"-C=somedir -other -args", "-C=somedir -o debug -gcflags 'all=-N -l' -other -args pkg", false},
		{"-C somedir", "-C somedir -c -o debug -gcflags 'all=-N -l' pkg", true},
		{"-C=somedir", "-C=somedir -c -o debug -gcflags 'all=-N -l' pkg", true},
	}

	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			out := goBuildArgs("debug", []string{"pkg"}, tc.in, tc.isTest)
			tgt := config.SplitQuotedFields(tc.tgt, '\'')
			t.Logf("%q -> %q", tc.in, out)
			if !reflect.DeepEqual(out, tgt) {
				t.Errorf("output mismatch input %q\noutput %q\ntarget %q", tc.in, out, tgt)
			}
		})
	}
}

func TestGoBuildArgs2DashC(t *testing.T) {
	testCases := []struct {
		in, tgt []string
		isTest  bool
	}{
		{
			[]string{"-C", "somedir", "-other"},
			[]string{"-C", "somedir", "-other", "-o", "debug", "-gcflags", "all=-N -l", "pkg"},
			false,
		},
		{
			[]string{"-C=somedir", "-other"},
			[]string{"-C=somedir", "-other", "-o", "debug", "-gcflags", "all=-N -l", "pkg"},
			false,
		},
		{
			[]string{"-C", "somedir", "-other"},
			[]string{"-C", "somedir", "-c", "-other", "-o", "debug", "-gcflags", "all=-N -l", "pkg"},
			true,
		},
		{
			[]string{"-C=somedir", "-other"},
			[]string{"-C=somedir", "-c", "-other", "-o", "debug", "-gcflags", "all=-N -l", "pkg"},
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s-test=%t", tc.in[0], tc.isTest), func(t *testing.T) {
			t.Parallel()

			out, err := goBuildArgs2("debug", []string{"pkg"}, tc.in, tc.isTest)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%q -> %q", tc.in, out)
			if !reflect.DeepEqual(out, tc.tgt) {
				t.Errorf("output mismatch input %q\noutput %q\ntarget %q", tc.in, out, tc.tgt)
			}
		})
	}
}
