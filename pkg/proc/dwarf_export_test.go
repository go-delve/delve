package proc

import "github.com/go-delve/delve/pkg/dwarf/op"

// PackageVars returns bi.packageVars (for tests)
func (bi *BinaryInfo) PackageVars() []packageVar {
	return bi.packageVars
}

func NewCompositeMemory(p *Target, pieces []op.Piece) (*compositeMemory, error) {
	regs, err := p.CurrentThread().Registers()
	if err != nil {
		return nil, err
	}

	arch := p.BinInfo().Arch
	dwarfregs := arch.RegistersToDwarfRegisters(0, regs)
	dwarfregs.ChangeFunc = p.CurrentThread().SetReg

	return newCompositeMemory(p.Memory(), arch, *dwarfregs, pieces)
}
