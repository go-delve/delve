package cmds

import (
	"testing"
)

func TestSplitQuotedFields(t *testing.T) {
	in := `field'A' 'fieldB' fie'l\'d'C fieldD 'another field' fieldE`
	tgt := []string{"fieldA", "fieldB", "fiel'dC", "fieldD", "another field", "fieldE"}
	out := splitQuotedFields(in)

	if len(tgt) != len(out) {
		t.Fatalf("expected %#v, got %#v (len mismatch)", tgt, out)
	}

	for i := range tgt {
		if tgt[i] != out[i] {
			t.Fatalf(" expected %#v, got %#v (mismatch at %d)", tgt, out, i)
		}
	}
}
