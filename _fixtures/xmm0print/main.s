#include "textflag.h"

TEXT ·VPSLLQ36(SB), NOSPLIT, $0-16
    MOVQ src+0(FP), AX
    MOVQ dst+8(FP), BX
    VMOVDQU (AX), Y0
    VPSLLQ $36, Y0, Y0
    VMOVDQU Y0, (BX)
    RET
