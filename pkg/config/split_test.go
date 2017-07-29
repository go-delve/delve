package config

import (
	"testing"
)

func TestSplitQuotedFields(t *testing.T) {
	in := `field'A' 'fieldB' fie'l\'d'C fieldD 'another field' fieldE`
	tgt := []string{"fieldA", "fieldB", "fiel'dC", "fieldD", "another field", "fieldE"}
	out := SplitQuotedFields(in, '\'')

	if len(tgt) != len(out) {
		t.Fatalf("expected %#v, got %#v (len mismatch)", tgt, out)
	}

	for i := range tgt {
		if tgt[i] != out[i] {
			t.Fatalf(" expected %#v, got %#v (mismatch at %d)", tgt, out, i)
		}
	}
}

func TestSplitDoubleQuotedFields(t *testing.T) {
	in := `field"A" "fieldB" fie"l'd"C "field\"D" "yet another field"`
	tgt := []string{"fieldA", "fieldB", "fiel'dC", "field\"D", "yet another field"}
	out := SplitQuotedFields(in, '"')

	if len(tgt) != len(out) {
		t.Fatalf("expected %#v, got %#v (len mismatch)", tgt, out)
	}

	for i := range tgt {
		if tgt[i] != out[i] {
			t.Fatalf(" expected %#v, got %#v (mismatch at %d)", tgt, out, i)
		}
	}
}
