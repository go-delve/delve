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
	tests := []struct {
		name     string
		in       string
		expected []string
	}{
		{
			name:     "generic test case",
			in:       `field"A" "fieldB" fie"l'd"C "field\"D" "yet another field"`,
			expected: []string{"fieldA", "fieldB", "fiel'dC", "field\"D", "yet another field"},
		},
		{
			name:     "with empty string in the end",
			in:       `field"A" "" `,
			expected: []string{"fieldA", ""},
		},
		{
			name:     "with empty string at the beginning",
			in:       ` "" field"A"`,
			expected: []string{"", "fieldA"},
		},
		{
			name:     "lots of spaces",
			in:       `    field"A"   `,
			expected: []string{"fieldA"},
		},
		{
			name:     "only empty string",
			in:       ` "" "" "" """" "" `,
			expected: []string{"", "", "", "", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			tgt := tt.expected
			out := SplitQuotedFields(in, '"')
			if len(tgt) != len(out) {
				t.Fatalf("expected %#v, got %#v (len mismatch)", tgt, out)
			}

			for i := range tgt {
				if tgt[i] != out[i] {
					t.Fatalf(" expected %#v, got %#v (mismatch at %d)", tgt, out, i)
				}
			}
		})
	}
}

func TestConfigureListByName(t *testing.T) {
	type testConfig struct {
		boolArg bool     `cfgName:"bool-arg"`
		listArg []string `cfgName:"list-arg"`
	}

	type args struct {
		sargs   *testConfig
		cfgname string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "basic bool",
			args: args{
				sargs: &testConfig{
					boolArg: true,
					listArg: []string{},
				},
				cfgname: "bool-arg",
			},
			want: "bool-arg	true\n",
		},
		{
			name: "list arg",
			args: args{
				sargs: &testConfig{
					boolArg: true,
					listArg: []string{"item 1", "item 2"},
				},

				cfgname: "list-arg",
			},
			want: "list-arg	[item 1 item 2]\n",
		},
		{
			name: "empty",
			args: args{
				sargs:   &testConfig{},
				cfgname: "",
			},
			want: "",
		},
		{
			name: "invalid",
			args: args{
				sargs:   &testConfig{},
				cfgname: "nonexistent",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigureListByName(tt.args.sargs, tt.args.cfgname, "cfgName"); got != tt.want {
				t.Errorf("ConfigureListByName() = %v, want %v", got, tt.want)
			}
		})
	}
}
