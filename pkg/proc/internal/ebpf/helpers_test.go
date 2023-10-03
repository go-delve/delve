//go:build linux && amd64 && cgo && go1.16

package ebpf

import (
	"reflect"
	"testing"

	"github.com/go-delve/delve/pkg/proc/internal/ebpf/testhelper"
)

func compareStructTypes(t *testing.T, gostructVal, cstructVal interface{}) {
	gostruct := reflect.ValueOf(gostructVal).Type()
	cstruct := reflect.ValueOf(cstructVal).Type()
	if gostruct.NumField() != cstruct.NumField() {
		t.Errorf("mismatched field number %d %d", gostruct.NumField(), cstruct.NumField())
		return
	}
	for i := 0; i < cstruct.NumField(); i++ {
		gofield := gostruct.Field(i)
		cfield := cstruct.Field(i)
		t.Logf("%d %s %s\n", i, gofield.Name, cfield.Name)
		if gofield.Name != cfield.Name {
			t.Errorf("mismatched name for field %s %s", gofield.Name, cfield.Name)
		}
		if gofield.Offset != cfield.Offset {
			t.Errorf("mismatched offset for field %s %s (%d %d)", gofield.Name, cfield.Name, gofield.Offset, cfield.Offset)
		}
		if gofield.Type.Size() != cfield.Type.Size() {
			t.Errorf("mismatched size for field %s %s (%d %d)", gofield.Name, cfield.Name, gofield.Type.Size(), cfield.Type.Size())
		}
	}
}

func TestStructConsistency(t *testing.T) {
	t.Run("function_parameter_t", func(t *testing.T) {
		compareStructTypes(t, function_parameter_t{}, testhelper.Function_parameter_t{})
	})
	t.Run("function_parameter_list_t", func(t *testing.T) {
		compareStructTypes(t, function_parameter_list_t{}, testhelper.Function_parameter_list_t{})
	})
}
