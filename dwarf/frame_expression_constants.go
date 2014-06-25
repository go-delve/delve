package frame

// Operation opcodes
const (
	DW_OP_addr    = 0x03
	DW_OP_const1s = 0x09
)

const (
	DW_OP_const2u = 0x0a
	DW_OP_const2s = 0x0b
	DW_OP_const4u = iota
	DW_OP_const4s
	DW_OP_const8u
	DW_OP_const8s
	DW_OP_constu
	DW_OP_consts
	DW_OP_dup
	DW_OP_drop
	DW_OP_over
	DW_OP_pick
	DW_OP_swap
	DW_OP_rot
	DW_OP_xderef
	DW_OP_abs
	DW_OP_and
	DW_OP_div
	DW_OP_minus
	DW_OP_mod
	DW_OP_mul
	DW_OP_neg
	DW_OP_not
	DW_OP_or
	DW_OP_plus
	DW_OP_plus_uconst
	DW_OP_shl
	DW_OP_shr
	DW_OP_shra
	DW_OP_xor
	DW_OP_skip
	DW_OP_bra
	DW_OP_eq
	DW_OP_ge
	DW_OP_gt
	DW_OP_le
	DW_OP_lt
	DW_OP_ne
)

const (
	DW_OP_lit0 = 0x30
	DW_OP_lit1 = 0x31
	DW_OP_lit2 = iota
	DW_OP_lit3
	DW_OP_lit4
	DW_OP_lit5
	DW_OP_lit6
	DW_OP_lit7
	DW_OP_lit8
	DW_OP_lit9
	DW_OP_lit10
	DW_OP_lit11
	DW_OP_lit12
	DW_OP_lit13
	DW_OP_lit14
	DW_OP_lit15
	DW_OP_lit16
	DW_OP_lit17
	DW_OP_lit18
	DW_OP_lit19
	DW_OP_lit20
	DW_OP_lit21
	DW_OP_lit22
	DW_OP_lit23
	DW_OP_lit24
	DW_OP_lit25
	DW_OP_lit26
	DW_OP_lit27
	DW_OP_lit28
	DW_OP_lit29
	DW_OP_lit30
	DW_OP_lit31
	DW_OP_reg0
	DW_OP_reg1
	DW_OP_reg2
	DW_OP_reg3
	DW_OP_reg4
	DW_OP_reg5
	DW_OP_reg6
	DW_OP_reg7
	DW_OP_reg8
	DW_OP_reg9
	DW_OP_reg10
	DW_OP_reg11
	DW_OP_reg12
	DW_OP_reg13
	DW_OP_reg14
	DW_OP_reg15
	DW_OP_reg16
	DW_OP_reg17
	DW_OP_reg18
	DW_OP_reg19
	DW_OP_reg20
	DW_OP_reg21
	DW_OP_reg22
	DW_OP_reg23
	DW_OP_reg24
	DW_OP_reg25
	DW_OP_reg26
	DW_OP_reg27
	DW_OP_reg28
	DW_OP_reg29
	DW_OP_reg30
	DW_OP_reg31
	DW_OP_breg0
	DW_OP_breg1
	DW_OP_breg2
	DW_OP_breg3
	DW_OP_breg4
	DW_OP_breg5
	DW_OP_breg6
	DW_OP_breg7
	DW_OP_breg8
	DW_OP_breg9
	DW_OP_breg10
	DW_OP_breg11
	DW_OP_breg12
	DW_OP_breg13
	DW_OP_breg14
	DW_OP_breg15
	DW_OP_breg16
	DW_OP_breg17
	DW_OP_breg18
	DW_OP_breg19
	DW_OP_breg20
	DW_OP_breg21
	DW_OP_breg22
	DW_OP_breg23
	DW_OP_breg24
	DW_OP_breg25
	DW_OP_breg26
	DW_OP_breg27
	DW_OP_breg28
	DW_OP_breg29
	DW_OP_breg30
	DW_OP_breg31
	DW_OP_regx
	DW_OP_fbreg
	DW_OP_bregx
	DW_OP_piece
	DW_OP_deref_size
	DW_OP_xderef_size
	DW_OP_nop
	DW_OP_push_object_address
	DW_OP_call2
	DW_OP_call4
	DW_OP_call_ref
	DW_OP_form_tls_address
	DW_OP_call_frame_cfa
	DW_OP_bit_piece

	DW_OP_lo_user = 0xe0
	DW_OP_hi_user = 0xff
)
