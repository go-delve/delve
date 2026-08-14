// FuzzEvalStackOps fuzzes the expression-evaluation stack machine (evalStack)
// using programs built from stack-mechanics opcodes only (constants, nil,
// pop/dup/roll, JumpAlways, PushLen, PushCurg, PushThreadID, PushFrameoff).
// It does not emit Binary, Select, BoolToConst, or call-injection ops: those
// need typed operands or a live target. Evaluation errors are expected; only
// unrecovered panics and errors containing "internal debugger error" fail
// the fuzz run. Programs that fail DepthCheck (underflow, inconsistent
// joins, out-of-range jumps) are skipped; end-depth is not required, unlike
// Compile.
//
//	go test -run NONE -fuzz=FuzzEvalStackOps -fuzztime=5s ./pkg/proc

package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"go/constant"
	"io"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc/evalop"
)

const (
	fuzzEvalStackMaxOps  = 32
	fuzzEvalStackNumTags = 10
)

func decodeEvalStackOps(buf []byte) (ops []evalop.Op) {
	if len(buf) == 0 {
		return nil
	}
	defer func() { remapEvalStackJumpTargets(ops) }()

	r := bytes.NewReader(buf)
	nByte, err := r.ReadByte()
	if err != nil {
		return nil
	}
	n := int(nByte)%fuzzEvalStackMaxOps + 1
	ops = make([]evalop.Op, 0, n)

	readByte := func() (byte, bool) {
		b, err := r.ReadByte()
		return b, err == nil
	}
	readFull := func(dst []byte) bool {
		_, err := io.ReadFull(r, dst)
		return err == nil
	}

	for len(ops) < n {
		tag, ok := readByte()
		if !ok {
			return ops
		}
		switch tag % fuzzEvalStackNumTags {
		case 0: // PushConst
			kind, ok := readByte()
			if !ok {
				return ops
			}
			switch kind % 3 {
			case 0: // int64
				var raw [8]byte
				if !readFull(raw[:]) {
					return ops
				}
				v := int64(binary.LittleEndian.Uint64(raw[:]))
				ops = append(ops, &evalop.PushConst{Value: constant.MakeInt64(v)})
			case 1: // bool
				b, ok := readByte()
				if !ok {
					return ops
				}
				ops = append(ops, &evalop.PushConst{Value: constant.MakeBool(b != 0)})
			default: // string
				lByte, ok := readByte()
				if !ok {
					return ops
				}
				l := int(lByte % 8)
				s := make([]byte, l)
				if !readFull(s) {
					return ops
				}
				ops = append(ops, &evalop.PushConst{Value: constant.MakeString(string(s))})
			}
		case 1:
			ops = append(ops, &evalop.PushNil{})
		case 2:
			ops = append(ops, &evalop.Pop{})
		case 3:
			ops = append(ops, &evalop.Dup{})
		case 4: // Roll
			b, ok := readByte()
			if !ok {
				return ops
			}
			ops = append(ops, &evalop.Roll{N: int(b % 8)})
		case 5: // JumpAlways — conditional jumps need a typed bool; DepthCheck
			// cannot validate kinds, so the decoder only emits JumpAlways.
			b, ok := readByte()
			if !ok {
				return ops
			}
			ops = append(ops, &evalop.Jump{When: evalop.JumpAlways, Target: int(b)})
		case 6:
			ops = append(ops, &evalop.PushLen{})
		case 7:
			ops = append(ops, &evalop.PushCurg{})
		case 8:
			ops = append(ops, &evalop.PushThreadID{})
		case 9:
			ops = append(ops, &evalop.PushFrameoff{})
		}
	}
	return ops
}

func remapEvalStackJumpTargets(ops []evalop.Op) {
	n := len(ops)
	if n == 0 {
		return
	}
	for _, op := range ops {
		if j, ok := op.(*evalop.Jump); ok {
			j.Target = j.Target % n
		}
	}
}

// evalStackOpsDepthOK reports whether ops are underflow-safe and have
// consistent join depths. End-depth is not checked (DepthCheck endDepth -1)
// so programs that leave extra values, including Roll, still run.
func evalStackOpsDepthOK(ops []evalop.Op) bool {
	return evalop.DepthCheck(ops, -1) == nil
}

func TestDecodeEvalStackOps(t *testing.T) {
	t.Run("EmptyInput", func(t *testing.T) {
		if ops := decodeEvalStackOps(nil); len(ops) != 0 {
			t.Fatalf("got %d ops", len(ops))
		}
	})

	t.Run("PushPop", func(t *testing.T) {
		// N=2, PushConst int 7, Pop
		buf := []byte{1}        // N = 1%32+1 = 2
		buf = append(buf, 0, 0) // PushConst kind int64
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], 7)
		buf = append(buf, b[:]...)
		buf = append(buf, 2) // Pop
		ops := decodeEvalStackOps(buf)
		if len(ops) != 2 {
			t.Fatalf("len=%d", len(ops))
		}
		c, ok := ops[0].(*evalop.PushConst)
		if !ok {
			t.Fatalf("op[0] type %T", ops[0])
		}
		if got, _ := constant.Int64Val(c.Value); got != 7 {
			t.Fatalf("const=%d", got)
		}
		if _, ok := ops[1].(*evalop.Pop); !ok {
			t.Fatalf("op[1] type %T", ops[1])
		}
	})

	t.Run("TruncatedPayloadStops", func(t *testing.T) {
		ops := decodeEvalStackOps([]byte{1, 1, 0, 0})
		if len(ops) != 1 {
			t.Fatalf("len=%d", len(ops))
		}
		if _, ok := ops[0].(*evalop.PushNil); !ok {
			t.Fatalf("op type %T", ops[0])
		}
	})

	t.Run("TagModuloContinues", func(t *testing.T) {
		// 253 % fuzzEvalStackNumTags == 3 (Dup). n=2: PushNil, Dup.
		ops := decodeEvalStackOps([]byte{1, 1, 253})
		if len(ops) != 2 {
			t.Fatalf("len=%d", len(ops))
		}
		if _, ok := ops[0].(*evalop.PushNil); !ok {
			t.Fatalf("op[0] type %T", ops[0])
		}
		if _, ok := ops[1].(*evalop.Dup); !ok {
			t.Fatalf("op[1] type %T", ops[1])
		}
	})

	t.Run("ConstKindModulo", func(t *testing.T) {
		buf := []byte{0, 0, 3} // n=1, PushConst, kind 3%3=0 (int64)
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], 7)
		buf = append(buf, b[:]...)
		ops := decodeEvalStackOps(buf)
		if len(ops) != 1 {
			t.Fatalf("len=%d", len(ops))
		}
		c, ok := ops[0].(*evalop.PushConst)
		if !ok {
			t.Fatalf("op type %T", ops[0])
		}
		if got, _ := constant.Int64Val(c.Value); got != 7 {
			t.Fatalf("const=%d", got)
		}
	})

	t.Run("JumpTargetRemappedToActualLen", func(t *testing.T) {
		// nByte=9 → intended n=10, but only one JumpAlways is present.
		ops := decodeEvalStackOps([]byte{9, 5, 9})
		if len(ops) != 1 {
			t.Fatalf("len=%d", len(ops))
		}
		j, ok := ops[0].(*evalop.Jump)
		if !ok {
			t.Fatalf("op type %T", ops[0])
		}
		if j.Target != 0 {
			t.Fatalf("target=%d, want 0", j.Target)
		}
	})

	t.Run("PushCurg", func(t *testing.T) {
		ops := decodeEvalStackOps([]byte{0, 7})
		if len(ops) != 1 {
			t.Fatalf("len=%d", len(ops))
		}
		if _, ok := ops[0].(*evalop.PushCurg); !ok {
			t.Fatalf("op type %T", ops[0])
		}
	})

	t.Run("RollSeedIsDepthOK", func(t *testing.T) {
		ops := decodeEvalStackOps([]byte{8, 1, 1, 1, 1, 1, 1, 1, 1, 4, 7})
		if len(ops) != 9 {
			t.Fatalf("len=%d", len(ops))
		}
		if !evalStackOpsDepthOK(ops) {
			t.Fatal("eight PushNil + Roll{7} must pass the underflow filter")
		}
	})
}

func stubEvalScopeForStackFuzz() *EvalScope {
	bi := NewBinaryInfo("linux", "amd64")
	return &EvalScope{
		Mem:     &zeroFillMemory{writeErr: errors.New("write not supported in stack fuzz stub")},
		BinInfo: bi,
		Regs:    op.DwarfRegisters{ByteOrder: binary.LittleEndian},
	}
}

func runEvalStackOps(ops []evalop.Op) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal debugger error: panic: %v", r)
		}
	}()
	stack := &evalStack{}
	stack.eval(stubEvalScopeForStackFuzz(), ops)
	return stack.err
}

func TestRunEvalStackOps(t *testing.T) {
	t.Run("DepthValidPushPop", func(t *testing.T) {
		ops := []evalop.Op{
			&evalop.PushConst{Value: constant.MakeInt64(1)},
			&evalop.Pop{},
		}
		FailIfInternalDebuggerError(t, runEvalStackOps(ops))
	})

	t.Run("EmptyPopIsInternalOrPanicRecovered", func(t *testing.T) {
		err := runEvalStackOps([]evalop.Op{&evalop.Pop{}})
		// Depth-invalid pop is recovered inside executeOp; this is not a
		// fuzz-corpus case. The harness must not unrecovered-panic.
		if err == nil {
			t.Fatal("expected error from empty pop")
		}
	})

	t.Run("EmptyFncallCompleteIsCleanError", func(t *testing.T) {
		err := runEvalStackOps([]evalop.Op{&evalop.CallInjectionComplete{DoPinning: false}})
		if err == nil {
			t.Fatal("expected error for CallInjectionComplete with no live call")
		}
		FailIfInternalDebuggerError(t, err)
		if !strings.Contains(err.Error(), "terminated before target") {
			t.Fatalf("expected terminated-before-target, got: %v", err)
		}
	})

	t.Run("EmptyFncallJumpIfPinningDoneIsCleanError", func(t *testing.T) {
		err := runEvalStackOps([]evalop.Op{
			&evalop.Jump{When: evalop.JumpIfPinningDone, Target: 1},
		})
		if err == nil {
			t.Fatal("expected error for JumpIfPinningDone with no live call")
		}
		FailIfInternalDebuggerError(t, err)
		if !strings.Contains(err.Error(), "terminated before target") {
			t.Fatalf("expected terminated-before-target, got: %v", err)
		}
	})

	// JumpAlways-to-self is depth-consistent but never suspends. The
	// production step limit must stop it; a 1-op program uses the minimum
	// budget (1024), not a huge constant that would dominate fuzz runtime.
	t.Run("JumpAlwaysToSelfTerminates", func(t *testing.T) {
		ops := []evalop.Op{
			&evalop.Jump{When: evalop.JumpAlways, Target: 0},
		}
		err := runEvalStackOps(ops)
		if err == nil {
			t.Fatal("expected step-limit error from JumpAlways-to-self")
		}
		if strings.Contains(err.Error(), "internal debugger error") {
			t.Fatalf("step-limit error must not be an internal debugger error: %v", err)
		}
		if !strings.Contains(err.Error(), "exceeded 1024 step limit") {
			t.Fatalf("expected minimum 1024-step limit, got: %v", err)
		}
	})
}

func FuzzEvalStackOps(f *testing.F) {
	// PushConst(7) + Pop
	var push7 [8]byte
	binary.LittleEndian.PutUint64(push7[:], 7)
	f.Add(append([]byte{1, 0, 0}, append(push7[:], 2)...))
	// Eight PushNil + Roll N=7 (underflow-safe, end depth 8)
	f.Add([]byte{8, 1, 1, 1, 1, 1, 1, 1, 1, 4, 7})
	// Two PushNil + Roll N=1 + Pop (end depth 1)
	f.Add([]byte{3, 1, 1, 4, 1, 2})
	// PushCurg
	f.Add([]byte{0, 7})

	f.Fuzz(func(t *testing.T, buf []byte) {
		ops := decodeEvalStackOps(buf)
		if !evalStackOpsDepthOK(ops) {
			return
		}
		FailIfInternalDebuggerError(t, runEvalStackOps(ops))
	})
}
