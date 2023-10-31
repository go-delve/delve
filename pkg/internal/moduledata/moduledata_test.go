package moduledata

import (
	"debug/elf"
	"os"
	"os/exec"
	"testing"
)

func TestGetGoFuncValue(t *testing.T) {
	bin := "getgofuncvaltestbin"

	err := exec.Command("go", "build", "-o", bin, "../../../_fixtures/traceprog.go").Run()
	if err != nil {
		t.Fatal(err)
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			t.Fatal(err)
		}
	}(bin)

	file, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}

	s := file.Section(".gopclntab")

	goFuncValue, err := GetGoFuncValue(file, s.Addr)
	if err != nil {
		t.Fatal(err)
	}

	goFuncSymValue := getGoFuncSymValue(file, t)
	if goFuncSymValue == 0 {
		t.Fatal("unable to find value for go:func.* symbol")
	}

	t.Logf("gofuncVal: %#v goFuncSymValue: %#v\n", goFuncValue, goFuncSymValue)

	if goFuncValue != goFuncSymValue {
		t.Fatalf("expected goFuncValue %#v to equal goFuncSymValue %#v", goFuncValue, goFuncSymValue)
	}
}

func getGoFuncSymValue(f *elf.File, t *testing.T) uint64 {
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for i := range syms {
		if syms[i].Name == "go:func.*" {
			return syms[i].Value
		}
	}
	return 0
}
