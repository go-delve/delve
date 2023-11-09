package api

import "testing"

func TestShortenType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"long/package/path/pkg.A", "pkg.A"},
		{"[]long/package/path/pkg.A", "[]pkg.A"},
		{"map[long/package/path/pkg.A]long/package/path/pkg.B", "map[pkg.A]pkg.B"},
		{"map[long/package/path/pkg.A]interface {}", "map[pkg.A]interface {}"},
		{"map[long/package/path/pkg.A]interface{}", "map[pkg.A]interface{}"},
		{"map[long/package/path/pkg.A]struct {}", "map[pkg.A]struct {}"},
		{"map[long/package/path/pkg.A]struct{}", "map[pkg.A]struct{}"},
		{"map[long/package/path/pkg.A]map[long/package/path/pkg.B]long/package/path/pkg.C", "map[pkg.A]map[pkg.B]pkg.C"},
		{"map[long/package/path/pkg.A][]long/package/path/pkg.B", "map[pkg.A][]pkg.B"},
		{"map[uint64]*github.com/aarzilli/dwarf5/dwarf.typeUnit", "map[uint64]*dwarf.typeUnit"},
		{"uint8", "uint8"},
		{"encoding/binary", "encoding/binary"},
		{"*github.com/go-delve/delve/pkg/proc.Target", "*proc.Target"},
		{"long/package/path/pkg.Parametric[long/package/path/pkg.A, map[long/package/path/pkg.B]long/package/path/pkg.A]", "pkg.Parametric[pkg.A, map[pkg.B]pkg.A]"},
		{"[]long/package/path/pkg.Parametric[long/package/path/pkg.A]", "[]pkg.Parametric[pkg.A]"},
		{"[24]long/package/path/pkg.A", "[24]pkg.A"},
		{"chan func()", "chan func()"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ShortenType(tt.input)
			if result != tt.expected {
				t.Errorf("shortenTypeEx() got = %v, want %v", result, tt.expected)
			}
		})
	}
}
