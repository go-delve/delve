package gobuild

import (
	"reflect"
	"testing"

	"github.com/go-delve/delve/pkg/config"
)

func TestGoBuildArgsDashC(t *testing.T) {
	testCases := []struct{ in, tgt string }{
		{"-C somedir", "-C somedir -o debug -gcflags 'all=-N -l' pkg"},
		{"-C", "-o debug -gcflags 'all=-N -l' -C pkg"},
		{"-C=somedir", "-C=somedir -o debug -gcflags 'all=-N -l' pkg"},
		{"-C somedir -other -args", "-C somedir -o debug -gcflags 'all=-N -l' -other -args pkg"},
		{"-C=somedir -other -args", "-C=somedir -o debug -gcflags 'all=-N -l' -other -args pkg"},
	}

	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			out := goBuildArgs("debug", []string{"pkg"}, tc.in, false)
			tgt := config.SplitQuotedFields(tc.tgt, '\'')
			t.Logf("%q -> %q", tc.in, out)
			if !reflect.DeepEqual(out, tgt) {
				t.Errorf("output mismatch input %q\noutput %q\ntarget %q", tc.in, out, tgt)
			}
		})
	}
}
