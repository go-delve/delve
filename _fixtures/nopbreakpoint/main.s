#include "textflag.h"

TEXT ·compromised(SB),NOSPLIT,$0-0
	BYTE	 $0x90   // The assembler strips NOP, this is a hardcoded NOP instruction
	CALL main·skipped(SB)
	RET
