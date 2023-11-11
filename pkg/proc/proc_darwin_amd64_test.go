package proc_test

import (
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc"

	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestSetYMMRegister(t *testing.T) {
	// Checks that setting a XMM register works. This checks that the
	// workaround for a bug in debugserver works.
	// See issue #2767.
	withTestProcess("setymmreg/", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.asmFunc")
		assertNoError(grp.Continue(), t, "Continue()")

		getReg := func(pos string) *op.DwarfRegister {
			regs := getRegisters(p, t)

			arch := p.BinInfo().Arch
			dregs := arch.RegistersToDwarfRegisters(0, regs)

			r := dregs.Reg(regnum.AMD64_XMM0)
			t.Logf("%s: %#v", pos, r)
			return r
		}

		getReg("before")

		p.CurrentThread().SetReg(regnum.AMD64_XMM0, op.DwarfRegisterFromBytes([]byte{
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44}))
		assertNoError(grp.StepInstruction(), t, "SetpInstruction")

		xmm0 := getReg("after")

		for i := range xmm0.Bytes {
			if xmm0.Bytes[i] != 0x44 {
				t.Fatalf("wrong register value")
			}
		}
	})
}
