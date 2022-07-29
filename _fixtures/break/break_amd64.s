#include "textflag.h"

TEXT ·asmBrk(SB),0,$0-0
	BYTE	$0xcc
	RET
