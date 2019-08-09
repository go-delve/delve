#include "textflag.h"

TEXT ·compromised(SB),NOSPLIT,$0-8
	CMPQ n+0(FP), $0
	JNZ notzero
	RET
notzero:
	MOVQ $0, AX
	MOVQ $1, AX
	CALL main·skipped(SB)
	RET
	