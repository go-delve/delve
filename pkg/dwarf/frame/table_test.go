package frame

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestExecuteDwarfProgram(t *testing.T) {
	// DW_CFA_advance_loc: 5
	// DW_CFA_def_cfa_offset: 16
	// DW_CFA_offset: r6 (rbp) at cfa-16
	// DW_CFA_advance_loc: 3
	// DW_CFA_def_cfa_register: r6 (rbp)
	// DW_CFA_advance_loc: 5
	// DW_CFA_offset: r3 (rbx) at cfa-24
	// DW_CFA_advance_loc: 42
	// DW_CFA_GNU_args_size: 32
	// DW_CFA_advance_loc1: 66
	// DW_CFA_def_cfa: r7 (rsp) ofs 8
	instructions, _ := hex.DecodeString("450e108602430d064583036a2e2002420c0708")
	frame := &FrameContext{
		Regs:            make(map[uint64]DWRule),
		initialRegs:     make(map[uint64]DWRule),
		codeAlignment:   1,
		dataAlignment:   -8,
		buf:             bytes.NewBuffer(instructions),
		rememberedState: newStateStack(),
	}

	frame.executeDwarfProgram()
	if frame.err != nil {
		t.Fatalf("Failed to execute DWARF program: %v", frame.err)
	}

	if frame.CFA.Rule != RuleCFA {
		t.Fatalf("Rule CFA expect: %v, got: %v", RuleCFA, frame.CFA.Rule)
	}

	if frame.CFA.Reg != 7 {
		t.Fatalf("CFA reg expect: %v, got: %v", 7, frame.CFA.Reg)
	}

	if frame.CFA.Offset != 8 {
		t.Fatalf("CFA offset expect: %v, got: %v", 8, frame.CFA.Offset)
	}

	if frame.Regs[6].Rule != RuleOffset {
		t.Fatalf("Rule r6 expect: %v, got: %v", RuleOffset, frame.Regs[6].Rule)
	}

	if frame.Regs[6].Offset != -16 {
		t.Fatalf("Reg r6 offset expect: %v, got: %v", -16, frame.Regs[6].Offset)
	}

	if frame.Regs[3].Rule != RuleOffset {
		t.Fatalf("Rule r3 expect: %v, got: %v", RuleOffset, frame.Regs[3].Rule)
	}

	if frame.Regs[3].Offset != -24 {
		t.Fatalf("Reg r3 offset expect: %v, got: %v", -24, frame.Regs[3].Offset)
	}
}
