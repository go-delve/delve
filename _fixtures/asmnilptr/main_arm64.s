#include "textflag.h"

TEXT ·asmFunc(SB),0,$0-16
	MOVD arg+0(FP), R5
	MOVD (R5), R5
	MOVD R5, ret+8(FP)
	RET
