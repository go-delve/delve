#include "textflag.h"

TEXT ·asmFunc(SB),0,$0-16
	MOV arg+0(FP), R5
	MOV (R5), R5
	MOV R5, ret+8(FP)
	RET