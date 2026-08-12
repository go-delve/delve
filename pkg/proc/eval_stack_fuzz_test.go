// FuzzEvalStackOps fuzzes the expression evaluation stack machine (evalStack).
// Evaluation errors are expected; only unrecovered panics and errors containing
// "internal debugger error" fail the fuzz run. Primary mode skips opcode
// programs that fail the static depthCheck filter:
//
//	go test -run NONE -fuzz=FuzzEvalStackOps -fuzztime=5s ./pkg/proc
//
// Unbounded mode runs all decoded programs (including depth-invalid):
//
//	go test -run NONE -fuzz=FuzzEvalStackOps -fuzzevalstackunbounded -fuzztime=5s ./pkg/proc
package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"go/constant"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/proc/evalop"
)

const fuzzEvalStackMaxOps = 32

func decodeEvalStackOps(buf []byte) []evalop.Op {
	if len(buf) == 0 {
		return nil
	}
	r := bytes.NewReader(buf)
	nByte, err := r.ReadByte()
	if err != nil {
		return nil
	}
	n := int(nByte)%fuzzEvalStackMaxOps + 1
	ops := make([]evalop.Op, 0, n)

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
		switch tag {
		case 0: // PushConst
			kind, ok := readByte()
			if !ok {
				return ops
			}
			switch kind {
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
			case 2: // string
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
			default:
				return ops
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
		case 5, 6, 7: // Jump
			b, ok := readByte()
			if !ok {
				return ops
			}
			when := evalop.JumpAlways
			if tag == 6 {
				when = evalop.JumpIfTrue
			} else if tag == 7 {
				when = evalop.JumpIfFalse
			}
			ops = append(ops, &evalop.Jump{When: when, Target: int(b) % n})
		case 8:
			ops = append(ops, &evalop.PushLen{})
		case 9:
			ops = append(ops, &evalop.BoolToConst{})
		default:
			return ops
		}
	}
	return ops
}

func evalStackOpsWhitelisted(ops []evalop.Op) bool {
	for _, op := range ops {
		switch op.(type) {
		case *evalop.PushConst, *evalop.PushNil, *evalop.Pop, *evalop.Dup, *evalop.Roll,
			*evalop.Jump, *evalop.PushLen, *evalop.BoolToConst:
			// ok — decoder only emits these; hand-written tests may not
		default:
			return false
		}
	}
	return true
}

// evalStackOpsDepthOK reports whether ops pass the same stack-depth checks
// Compile uses (evalop.DepthCheck), with either expression end-depth (1) or
// assignment end-depth (0).
func evalStackOpsDepthOK(ops []evalop.Op) bool {
	if !evalStackOpsWhitelisted(ops) {
		return false
	}
	return evalop.DepthCheck(ops, 1) == nil || evalop.DepthCheck(ops, 0) == nil
}

func TestDecodeEvalStackOps_EmptyInput(t *testing.T) {
	if ops := decodeEvalStackOps(nil); len(ops) != 0 {
		t.Fatalf("got %d ops", len(ops))
	}
}

func TestDecodeEvalStackOps_PushPop(t *testing.T) {
	// N=2, PushConst int 7, Pop  → tags/payloads as above
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
}

func TestDecodeEvalStackOps_TruncatedPayloadStops(t *testing.T) {
	buf := []byte{1, 1, 0, 0}
	ops := decodeEvalStackOps(buf)
	if len(ops) != 1 {
		t.Fatalf("len=%d", len(ops))
	}
	if _, ok := ops[0].(*evalop.PushNil); !ok {
		t.Fatalf("op type %T", ops[0])
	}
}

func TestDecodeEvalStackOps_UnknownTagStops(t *testing.T) {
	buf := []byte{1, 1, 255}
	ops := decodeEvalStackOps(buf)
	if len(ops) != 1 {
		t.Fatalf("len=%d", len(ops))
	}
	if _, ok := ops[0].(*evalop.PushNil); !ok {
		t.Fatalf("op type %T", ops[0])
	}
}

func TestEvalStackOpsDepthOK_UnderflowRejected(t *testing.T) {
	ops := []evalop.Op{&evalop.Pop{}}
	if evalStackOpsDepthOK(ops) {
		t.Fatal("expected reject")
	}
}

func TestEvalStackOpsDepthOK_PushPopOK(t *testing.T) {
	ops := []evalop.Op{
		&evalop.PushConst{Value: constant.MakeInt64(1)},
		&evalop.Pop{},
	}
	if !evalStackOpsDepthOK(ops) {
		t.Fatal("expected ok")
	}
}

func TestEvalStackOpsDepthOK_ConditionalJumpPopUnderflowRejected(t *testing.T) {
	ops := []evalop.Op{
		&evalop.PushConst{Value: constant.MakeBool(true)},
		&evalop.Jump{When: evalop.JumpIfTrue, Pop: true, Target: 2},
		&evalop.Pop{},
	}
	if evalStackOpsDepthOK(ops) {
		t.Fatal("expected reject")
	}
}

func TestEvalStackOpsDepthOK_InconsistentJoinRejected(t *testing.T) {
	ops := []evalop.Op{
		&evalop.PushNil{},
		&evalop.Jump{When: evalop.JumpAlways, Target: 3},
		&evalop.PushNil{},
		&evalop.Pop{},
	}
	if evalStackOpsDepthOK(ops) {
		t.Fatal("expected reject")
	}
}

func TestEvalStackOpsDepthOK_NonWhitelistRejected(t *testing.T) {
	ops := []evalop.Op{&evalop.PushIdent{Name: "x"}}
	if evalStackOpsDepthOK(ops) {
		t.Fatal("expected reject")
	}
}

func TestRunEvalStackOps_DepthValidPushPop(t *testing.T) {
	ops := []evalop.Op{
		&evalop.PushConst{Value: constant.MakeInt64(1)},
		&evalop.Pop{},
	}
	err := runEvalStackOps(ops)
	failIfInternalDebuggerError(t, err)
}

func TestRunEvalStackOps_EmptyPopIsInternalOrPanicRecovered(t *testing.T) {
	err := runEvalStackOps([]evalop.Op{&evalop.Pop{}})
	// Without depth filter this may be internal debugger error; harness must not unrecovered-panic.
	if err == nil {
		t.Fatal("expected error from empty pop")
	}
}

type zeroReaderStub struct{}

func (*zeroReaderStub) ReadMemory(b []byte, addr uint64) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

func (*zeroReaderStub) WriteMemory(uint64, []byte) (int, error) {
	return 0, errors.New("write not supported in stack fuzz stub")
}

func stubEvalScopeForStackFuzz() *EvalScope {
	bi := NewBinaryInfo("linux", "amd64")
	return &EvalScope{
		Mem:     &zeroReaderStub{},
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

// TestEvalStack_JumpAlwaysToSelf_Terminates locks the production evalStack
// interpreter against infinite JumpAlways cycles (depthCheck-valid programs
// that never suspend via call injection). The fuzz seed {0,5,0} encodes the
// same program.
func TestEvalStack_JumpAlwaysToSelf_Terminates(t *testing.T) {
	ops := []evalop.Op{
		&evalop.Jump{When: evalop.JumpAlways, Target: 0},
	}
	done := make(chan error, 1)
	go func() {
		stack := &evalStack{}
		stack.eval(stubEvalScopeForStackFuzz(), ops)
		done <- stack.err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected step-limit error from JumpAlways-to-self")
		}
		if strings.Contains(err.Error(), "internal debugger error") {
			t.Fatalf("step-limit error must not be an internal debugger error: %v", err)
		}
		if !strings.Contains(err.Error(), "step limit") {
			t.Fatalf("expected step limit error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("eval hung on JumpAlways-to-self; evalStack.run needs a step limit")
	}
}

var fuzzEvalStackUnbounded = flag.Bool("fuzzevalstackunbounded", false, "run FuzzEvalStackOps without depthCheck filter")

func FuzzEvalStackOps(f *testing.F) {
	// PushConst(7) + Pop
	var push7 [8]byte
	binary.LittleEndian.PutUint64(push7[:], 7)
	f.Add(append([]byte{1, 0, 0}, append(push7[:], 2)...))
	// Pop-only (depth-invalid; skipped in primary mode)
	f.Add([]byte{0, 2})
	// JumpAlways to self
	f.Add([]byte{0, 5, 0})
	// Eight PushNil + Roll N=7 (depth-valid)
	f.Add([]byte{8, 1, 1, 1, 1, 1, 1, 1, 1, 4, 7})

	f.Fuzz(func(t *testing.T, buf []byte) {
		ops := decodeEvalStackOps(buf)
		if !*fuzzEvalStackUnbounded && !evalStackOpsDepthOK(ops) {
			return
		}
		failIfInternalDebuggerError(t, runEvalStackOps(ops))
	})
}
