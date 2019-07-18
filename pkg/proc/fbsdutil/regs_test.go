package fbsdutil

import (
	"testing"

	"golang.org/x/arch/x86/x86asm"
)

func TestAMD64Get(t *testing.T) {
	val := int64(0x7fffffffdeadbeef)
	regs := AMD64Registers{
		Regs: &AMD64PtraceRegs{
			Rax: val,
		},
	}
	// Test AL, low 8 bits of RAX
	al, err := regs.Get(int(x86asm.AL))
	if err != nil {
		t.Fatal(err)
	}
	if al != 0xef {
		t.Fatalf("expected %#v, got %#v\n", 0xef, al)
	}

	// Test AH, high 8 bits of RAX
	ah, err := regs.Get(int(x86asm.AH))
	if err != nil {
		t.Fatal(err)
	}
	if ah != 0xBE {
		t.Fatalf("expected %#v, got %#v\n", 0xbe, ah)
	}

	// Test AX, lower 16 bits of RAX
	ax, err := regs.Get(int(x86asm.AX))
	if err != nil {
		t.Fatal(err)
	}
	if ax != 0xBEEF {
		t.Fatalf("expected %#v, got %#v\n", 0xbeef, ax)
	}

	// Test EAX, lower 32 bits of RAX
	eax, err := regs.Get(int(x86asm.EAX))
	if err != nil {
		t.Fatal(err)
	}
	if eax != 0xDEADBEEF {
		t.Fatalf("expected %#v, got %#v\n", 0xdeadbeef, eax)
	}

	// Test RAX, full 64 bits of register
	rax, err := regs.Get(int(x86asm.RAX))
	if err != nil {
		t.Fatal(err)
	}
	if rax != uint64(val) {
		t.Fatalf("expected %#v, got %#v\n", val, rax)
	}
}
