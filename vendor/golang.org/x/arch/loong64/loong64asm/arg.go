// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loong64asm

// Naming for Go decoder arguments:
//
// - arg_fd: a Floating Point operand register fd encoded in the fd[4:0] field
//
// - arg_fj: a Floating Point operand register fj encoded in the fj[9:5] field
//
// - arg_fk: a Floating Point operand register fk encoded in the fk[14:10] field
//
// - arg_fa: a Floating Point operand register fa encoded in the fa[19:15] field
//
// - arg_rd: a general-purpose register rd encoded in the rd[4:0] field
//
// - arg_rj: a general-purpose register rj encoded in the rj[9:5] field
//
// - arg_rk: a general-purpose register rk encoded in the rk[14:10] field
//
// - arg_fcsr_4_0: float control status register encoded in [4:0] field
//
// - arg_cd_2_0: condition flag register encoded in [2:0] field
//
// - arg_sa2_16_15: shift bits constant encoded in [16:15] field
//
// - arg_code_14_0: arg for exception process routine encoded in [14:0] field
//
// - arg_ui5_14_10: 5bits unsigned immediate
//
// - arg_bstrins_lsbw: For details, please refer to chapter 2.2.3.8 of instruction manual
//
// - arg_bstrpick_msbw: For details, please refer to chapter 2.2.3.9 of instruction manual
//
// - arg_preld_hint_4_0: hint field implied the prefetch type and the data should fetch to cache's level
//		0: load to data cache level 1
//		8: store to data cache level 1
//		other: no define
//
// - arg_si12_21_10: 12bits signed immediate

type instArg uint16

const (
	_ instArg = iota
	//1-5
	arg_fd
	arg_fj
	arg_fk
	arg_fa
	arg_rd
	arg_rd_
	//6-10
	arg_rj
	arg_rk
	arg_fcsr_4_0
	arg_fcsr_9_5
	arg_cd
	//11-15
	arg_cj
	arg_ca
	arg_sa2_16_15
	arg_sa2_16_15_bytepickw
	arg_sa3_17_15
	//16-20
	arg_code_14_0
	arg_ui5_14_10
	arg_ui6_15_10
	arg_ui12_21_10
	arg_bstrins_lsbw
	//21-25
	arg_bstrpick_msbw
	arg_bstrins_lsbd
	arg_bstrpick_msbd
	arg_preld_hint_4_0
	arg_hint_14_0
	//26-30
	arg_si12_21_10
	arg_si14_23_10
	arg_si16_25_10
	arg_si20_24_5
	arg_offset_20_0
	//31~
	arg_offset_25_0
	arg_offset_15_0
)
