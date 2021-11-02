// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loong64asm

import (
	"fmt"
	"strings"
)

// An Inst is a single instruction.
type Inst struct {
	Op   Op     // Opcode mnemonic
	Enc  uint32 // Raw encoding bits.
	Args Args   // Instruction arguments, in LOONG64 manual order.
}

func (i Inst) String() string {
	var args []string

	for _, arg := range i.Args {
		if arg == nil {
			break
		}

		args = append(args, arg.String())
	}

	str2 := strings.Join(args, ", ")
	if str2 == "" {
		str := i.Op.String()
		return strings.Replace(str, ", (", "(", -1)
	} else {
		str := i.Op.String() + " " + strings.Join(args, ", ")
		return strings.Replace(str, ", (", "(", -1)
	}
}

// An Op is an LOONG64 opcode.
type Op uint16

// NOTE: The actual Op values are defined in tables.go.
// They are chosen to simplify instruction decoding and
// are not a dense packing from 0 to N, although the
// density is high, probably at least 90%.
func (op Op) String() string {
	if (op >= Op(len(opstr))) || (opstr[op] == "") {
		return fmt.Sprintf("Op(%d)", int(op))
	}

	return opstr[op]
}

// An Args holds the instruction arguments.
// If an instruction has fewer than 5 arguments,
// the final elements in the array are nil.
type Args [5]Arg

// An Arg is a single instruction argument
type Arg interface {
	isArg()
	String() string
}

// A Reg is a single register.
// The zero value denotes R0, not the absence of a register.
// General-purpose register an float register
type Reg uint16

const (
	//_ Reg = iota

	// General-purpose register
	R0 Reg = iota
	R1
	R2
	R3
	R4
	R5
	R6
	R7
	R8
	R9
	R10
	R11
	R12
	R13
	R14
	R15
	R16
	R17
	R18
	R19
	R20
	R21
	R22
	R23
	R24
	R25
	R26
	R27
	R28
	R29
	R30
	R31

	// Float point register
	F0
	F1
	F2
	F3
	F4
	F5
	F6
	F7
	F8
	F9
	F10
	F11
	F12
	F13
	F14
	F15
	F16
	F17
	F18
	F19
	F20
	F21
	F22
	F23
	F24
	F25
	F26
	F27
	F28
	F29
	F30
	F31
)

func (Reg) isArg() {}

// LOONG64 LP64
func (r Reg) String() string {
	switch {
	case r == R0:
		// zero
		return "zero"

	case r == R1:
		// at
		return "ra"

	case r == R2:
		// tp
		return "tp"

	case r == R3:
		// sp
		return "sp"

	case (r >= R4) && (r <= R11):
		// a0 - a7/v0 - v1
		return fmt.Sprintf("a%d", int(r-R4))

	case (r >= R12) && (r <= R20):
		// t0 - t8
		return fmt.Sprintf("t%d", int(r-R12))

	case r == R21:
		// x
		return "x"

	case r == R22:
		// fp
		return "fp"

	case (r >= R23) && (r <= R31):
		// s0 - s8
		return fmt.Sprintf("s%d", int(r-R23))

	case (r >= F0) && (r <= F7):
		// fa0 - fa7
		return fmt.Sprintf("fa%d", int(r-F0))

	case (r >= F8) && (r <= F23):
		// ft0 - ft15
		return fmt.Sprintf("ft%d", int(r-F8))

	case (r >= F24) && (r <= F31):
		// fs0 - fs7
		return fmt.Sprintf("fs%d", int(r-F24))

	default:
		return fmt.Sprintf("Unknown(%d)", int(r))
	}
}

// float control status register
type Fcsr uint8

const (
	//_ Fcsr = iota
	FCSR0 Fcsr = iota
	FCSR1
	FCSR2
	FCSR3
)

func (Fcsr) isArg() {}

func (f Fcsr) String() string {
	return fmt.Sprintf("r%d", uint8(f))
}

// float condition flags register
type Fcc uint8

func (Fcc) isArg() {}

const (
	//_ Fcc = iota
	FCC0 Fcc = iota
	FCC1
	FCC2
	FCC3
	FCC4
	FCC5
	FCC6
	FCC7
)

func (f Fcc) String() string {
	return fmt.Sprintf("fcc%d", uint8(f))
}

// An Imm is an integer constant.
type Imm struct {
	Imm     uint32
	Decimal bool
}

func (Imm) isArg() {}

func (i Imm) String() string {
	if !i.Decimal {
		return fmt.Sprintf("%#x", i.Imm)
	} else {
		return fmt.Sprintf("%d", i.Imm)
	}
}

type Imm16 struct {
	Imm     int16
	Decimal bool
}

func (Imm16) isArg() {}

func (i Imm16) String() string {
	if !i.Decimal {
		return fmt.Sprintf("%#x", i.Imm)
	} else {
		return fmt.Sprintf("%d", i.Imm)
	}
}

type Imm32 struct {
	Imm     int32
	Decimal bool
}

func (Imm32) isArg() {}

func (i Imm32) String() string {
	if !i.Decimal {
		return fmt.Sprintf("%#x", i.Imm)
	} else {
		return fmt.Sprintf("%d", i.Imm)
	}
}

type Offset int32

func (Offset) isArg() {}

func (i Offset) String() string {
	return fmt.Sprintf("%d", int32(i))
}

type Sa int16

func (Sa) isArg() {}

func (i Sa) String() string {
	return fmt.Sprintf("%d", int16(i))
}

type Code int16

func (Code) isArg() {}

func (c Code) String() string {
	return fmt.Sprintf("%d", int16(c))
}
