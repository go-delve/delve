#include "textflag.h"

TEXT ·asmFunc(SB),0,$0-16
	MOVV arg+0(FP), R5
	MOVV (R5), R5
	MOVV R5, ret+8(FP)
	RET
