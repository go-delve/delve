#include "textflag.h"

TEXT ·asmFunc(SB),0,$0-16
	MOVL arg+0(FP), AX
	MOVL (AX), AX
	MOVL AX, ret+4(FP)
	RET
