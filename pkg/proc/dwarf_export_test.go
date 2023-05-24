package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"golang.org/x/arch/x86/x86asm"
)

// PackageVars returns bi.packageVars (for tests)
func (bi *BinaryInfo) PackageVars() []packageVar {
	return bi.packageVars
}

func NewCompositeMemory(p *Target, pieces []op.Piece, base uint64) (*compositeMemory, error) {
	regs, err := p.CurrentThread().Registers()
	if err != nil {
		return nil, err
	}

	arch := p.BinInfo().Arch
	dwarfregs := arch.RegistersToDwarfRegisters(0, regs)
	dwarfregs.ChangeFunc = p.CurrentThread().SetReg

	mem, err := newCompositeMemory(p.Memory(), arch, *dwarfregs, pieces, 0)
	if mem != nil {
		mem.base = base
	}
	return mem, err
}

func IsJNZ(inst archInst) bool {
	return inst.(*x86Inst).Op == x86asm.JNE
}
