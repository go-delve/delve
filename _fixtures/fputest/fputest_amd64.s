TEXT ·fputestsetup(SB),$0-48
	// setup x87 stack
	FMOVD f64a+0(FP), F0
	FMOVD f64b+8(FP), F0
	FMOVD f64c+16(FP), F0
	FMOVD f64d+24(FP), F0
	
	FMOVF f32a+32(FP), F0
	FMOVF f32b+36(FP), F0
	FMOVF f32c+40(FP), F0
	FMOVF f32d+44(FP), F0
	
	// setup SSE registers
	// XMM0 = { f64b, f64a } = { 1.2, 1.1 }
	MOVLPS f64a+0(FP), X0
	MOVHPS f64b+8(FP), X0
	
	// XMM1 = { f64d, f64c } = { 1.4, 1.3 }
	MOVLPS f64c+16(FP), X1
	MOVHPS f64d+24(FP), X1
	
	// XMM2 = { f32d, f32c, f32b, f32a } = { 1.8, 1.7, 1.6, 1.5 }
	MOVQ f32a+32(FP), AX
	MOVQ AX, X2
	MOVQ f32c+40(FP), AX
	MOVQ AX, X3
	PUNPCKLQDQ X3, X2
	
	// XMM3 = { f64a, f64b } = { 1.1, 1.2 }
	MOVLPS f64b+8(FP), X3
	MOVHPS f64a+0(FP), X3
	
	// XMM4 = { f64c, f64d } = { 1.3, 1.4 }
	MOVLPS f64d+24(FP), X4
	MOVHPS f64c+16(FP), X4
	
	// XMM5 = { f32b, f32a, f32d, f32c } = { 1.6, 1.5, 1.8, 1.7 }
	MOVQ f32c+40(FP), AX
	MOVQ AX, X5
	MOVQ f32a+32(FP), AX
	MOVQ AX, X6
	PUNPCKLQDQ X6, X5
	
	// XMM6 = XMM0 + XMM1 = { f64b+f64d, f64a+f64c } = { 2.6, 2.4 }
	MOVAPS X0,X6
	ADDPD X1, X6
	
	// XMM7 = XMM0 + XMM3 = { f64b+f64a, f64a+f64b } = { 2.3, 2.3 }
	MOVAPS X0, X7
	ADDPD X3, X7
	
	// XMM8 = XMM2 + XMM5 = { f32d+f32b, f32c+f32a, f32b+f32d, f32a+f32c } = { 3.4, 3.2, 3.4, 3.2 }
	MOVAPS X2, X8
	ADDPS X5, X8

	MOVAPS X1, X9
	MOVAPS X2, X10

	RET
