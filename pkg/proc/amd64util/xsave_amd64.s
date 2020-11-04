TEXT ·cpuid(SB),$0-24
	MOVL axIn+0(FP), AX
	MOVL cxIn+4(FP), CX
	CPUID
	MOVL AX, axOut+8(FP)
	MOVL BX, bxOut+12(FP)
	MOVL CX, cxOut+16(FP)
	MOVL DX, dxOut+20(FP)
	RET
