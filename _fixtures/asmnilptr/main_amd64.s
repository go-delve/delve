#include "textflag.h"

TEXT ·asmFunc(SB),0,$0-16
	MOVQ arg+0(FP), AX
	MOVQ (AX), AX
	MOVQ AX, ret+8(FP)
	RET
