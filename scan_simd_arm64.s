//go:build arm64 && !purego

#include "textflag.h"

// func indexQuoteOrBackslashNEON(data []byte, i int) int
TEXT ·indexQuoteOrBackslashNEON(SB), NOSPLIT, $0-40
	MOVD	data_base+0(FP), R0
	MOVD	data_len+8(FP), R1
	MOVD	i+24(FP), R2

	CMP	R1, R2
	BHS	iq_fail

	ADD	R2, R0, R3
	SUB	R2, R1, R4

	MOVD	$0x22, R5
	VMOV	R5, V0.B16
	MOVD	$0x5c, R6
	VMOV	R6, V1.B16

	CMP	$16, R4
	BLT	iq_tail

iq_loop16:
	VLD1	(R3), [V2.B16]
	VCMEQ	V0.B16, V2.B16, V3.B16
	VCMEQ	V1.B16, V2.B16, V4.B16
	VORR	V4.B16, V3.B16, V3.B16
	VADDP	V3.D2, V3.D2, V5.D2
	VMOV	V5.D[0], R7
	CBNZ	R7, iq_found16
	ADD	$16, R3, R3
	SUB	$16, R4, R4
	CMP	$16, R4
	BGE	iq_loop16

iq_tail:
	CBZ	R4, iq_fail
iq_tail_loop:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	iq_hit
	CMP	$0x5c, R7
	BEQ	iq_hit
	ADD	$1, R3, R3
	SUBS	$1, R4, R4
	BNE	iq_tail_loop
	B	iq_fail

iq_found16:
	MOVD	$16, R4
iq_found_scan:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	iq_hit
	CMP	$0x5c, R7
	BEQ	iq_hit
	ADD	$1, R3, R3
	SUBS	$1, R4, R4
	BNE	iq_found_scan
	B	iq_fail

iq_hit:
	MOVD	data_base+0(FP), R0
	SUB	R0, R3, R0
	MOVD	R0, ret+32(FP)
	RET

iq_fail:
	MOVD	$-1, R0
	MOVD	R0, ret+32(FP)
	RET

// func skipStringBodyNEON(data []byte, i int) (int, bool)
// i is the first byte inside the quotes. Returns index past closing quote.
TEXT ·skipStringBodyNEON(SB), NOSPLIT, $0-41
	MOVD	data_base+0(FP), R0
	MOVD	data_len+8(FP), R1
	MOVD	i+24(FP), R2

	CMP	R1, R2
	BHS	ss_fail

	ADD	R2, R0, R3
	SUB	R2, R1, R4

	MOVD	$0x22, R5
	VMOV	R5, V0.B16
	MOVD	$0x5c, R6
	VMOV	R6, V1.B16

ss_loop:
	CMP	$16, R4
	BLT	ss_scalar

	VLD1	(R3), [V2.B16]
	VCMEQ	V0.B16, V2.B16, V3.B16
	VCMEQ	V1.B16, V2.B16, V4.B16
	VORR	V4.B16, V3.B16, V3.B16
	VADDP	V3.D2, V3.D2, V5.D2
	VMOV	V5.D[0], R7
	CBNZ	R7, ss_hitblock

	ADD	$16, R3, R3
	SUB	$16, R4, R4
	B	ss_loop

ss_hitblock:
	MOVD	$16, R8
ss_scan:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	ss_quote
	CMP	$0x5c, R7
	BEQ	ss_escape
	ADD	$1, R3, R3
	SUB	$1, R4, R4
	SUBS	$1, R8, R8
	BNE	ss_scan
	B	ss_loop

ss_escape:
	CMP	$2, R4
	BLT	ss_fail
	ADD	$2, R3, R3
	SUB	$2, R4, R4
	B	ss_loop

ss_quote:
	ADD	$1, R3, R3
	MOVD	data_base+0(FP), R0
	SUB	R0, R3, R0
	MOVD	R0, ret+32(FP)
	MOVW	$1, R9
	MOVB	R9, ret1+40(FP)
	RET

ss_scalar:
	CBZ	R4, ss_fail
ss_scalar_loop:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	ss_quote
	CMP	$0x5c, R7
	BEQ	ss_escape
	ADD	$1, R3, R3
	SUBS	$1, R4, R4
	BNE	ss_scalar_loop

ss_fail:
	MOVD	data_len+8(FP), R0
	MOVD	R0, ret+32(FP)
	MOVB	ZR, ret1+40(FP)
	RET

// func indexStructuralOrQuoteNEON(data []byte, i int) int
// Find next " { } [ ] : , starting at i. Returns -1 if none.
TEXT ·indexStructuralOrQuoteNEON(SB), NOSPLIT, $0-40
	MOVD	data_base+0(FP), R0
	MOVD	data_len+8(FP), R1
	MOVD	i+24(FP), R2
	CMP	R1, R2
	BHS	isq_fail
	ADD	R2, R0, R3
	SUB	R2, R1, R4

	MOVD	$0x22, R5
	VMOV	R5, V0.B16 // "
	MOVD	$0x7b, R5
	VMOV	R5, V1.B16 // {
	MOVD	$0x7d, R5
	VMOV	R5, V2.B16 // }
	MOVD	$0x5b, R5
	VMOV	R5, V3.B16 // [
	MOVD	$0x5d, R5
	VMOV	R5, V4.B16 // ]
	MOVD	$0x3a, R5
	VMOV	R5, V5.B16 // :
	MOVD	$0x2c, R5
	VMOV	R5, V6.B16 // ,

	CMP	$16, R4
	BLT	isq_tail

isq_loop:
	VLD1	(R3), [V7.B16]
	VCMEQ	V0.B16, V7.B16, V16.B16
	VCMEQ	V1.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VCMEQ	V2.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VCMEQ	V3.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VCMEQ	V4.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VCMEQ	V5.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VCMEQ	V6.B16, V7.B16, V17.B16
	VORR	V17.B16, V16.B16, V16.B16
	VADDP	V16.D2, V16.D2, V17.D2
	VMOV	V17.D[0], R7
	CBNZ	R7, isq_hit
	ADD	$16, R3, R3
	SUB	$16, R4, R4
	CMP	$16, R4
	BGE	isq_loop

isq_tail:
	CBZ	R4, isq_fail
isq_tail_loop:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	isq_found
	CMP	$0x7b, R7
	BEQ	isq_found
	CMP	$0x7d, R7
	BEQ	isq_found
	CMP	$0x5b, R7
	BEQ	isq_found
	CMP	$0x5d, R7
	BEQ	isq_found
	CMP	$0x3a, R7
	BEQ	isq_found
	CMP	$0x2c, R7
	BEQ	isq_found
	ADD	$1, R3, R3
	SUBS	$1, R4, R4
	BNE	isq_tail_loop
	B	isq_fail

isq_hit:
	MOVD	$16, R4
isq_scan:
	MOVBU	(R3), R7
	CMP	$0x22, R7
	BEQ	isq_found
	CMP	$0x7b, R7
	BEQ	isq_found
	CMP	$0x7d, R7
	BEQ	isq_found
	CMP	$0x5b, R7
	BEQ	isq_found
	CMP	$0x5d, R7
	BEQ	isq_found
	CMP	$0x3a, R7
	BEQ	isq_found
	CMP	$0x2c, R7
	BEQ	isq_found
	ADD	$1, R3, R3
	SUBS	$1, R4, R4
	BNE	isq_scan
	B	isq_fail

isq_found:
	MOVD	data_base+0(FP), R0
	SUB	R0, R3, R0
	MOVD	R0, ret+32(FP)
	RET

isq_fail:
	MOVD	$-1, R0
	MOVD	R0, ret+32(FP)
	RET

// func skipContainerNEON(data []byte, i int) (int, bool)
TEXT ·skipContainerNEON(SB), NOSPLIT, $0-41
	MOVD	data_base+0(FP), R0
	MOVD	data_len+8(FP), R1
	MOVD	i+24(FP), R2

	CMP	R1, R2
	BHS	sc_fail

	ADD	$1, R2, R2
	MOVD	$1, R5
	ADD	R2, R0, R3
	ADD	R1, R0, R4

	MOVD	$0x2222222222222222, R10
	MOVD	$0x0101010101010101, R12
	MOVD	$0x8080808080808080, R13
	MOVD	$0x22, R6
	VMOV	R6, V0.B16

sc_loop:
	CMP	R4, R3
	BHS	sc_fail
	MOVBU	(R3), R6
sc_dispatch:
	CMP	$0x22, R6
	BEQ	sc_string
	CMP	$0x7b, R6
	BEQ	sc_open
	CMP	$0x7d, R6
	BEQ	sc_close
	CMP	$0x5b, R6
	BEQ	sc_open
	CMP	$0x5d, R6
	BEQ	sc_close
	ADD	$1, R3, R3

sc_bulk:
	ADD	$4, R3, R7
	CMP	R4, R7
	BHI	sc_loop
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	sc_loop
	CMP	$0x7b, R6
	BEQ	sc_loop
	CMP	$0x7d, R6
	BEQ	sc_loop
	CMP	$0x5b, R6
	BEQ	sc_loop
	CMP	$0x5d, R6
	BEQ	sc_loop
	MOVBU	1(R3), R6
	CMP	$0x22, R6
	BEQ	sc_b1
	CMP	$0x7b, R6
	BEQ	sc_b1
	CMP	$0x7d, R6
	BEQ	sc_b1
	CMP	$0x5b, R6
	BEQ	sc_b1
	CMP	$0x5d, R6
	BEQ	sc_b1
	MOVBU	2(R3), R6
	CMP	$0x22, R6
	BEQ	sc_b2
	CMP	$0x7b, R6
	BEQ	sc_b2
	CMP	$0x7d, R6
	BEQ	sc_b2
	CMP	$0x5b, R6
	BEQ	sc_b2
	CMP	$0x5d, R6
	BEQ	sc_b2
	MOVBU	3(R3), R6
	CMP	$0x22, R6
	BEQ	sc_b3
	CMP	$0x7b, R6
	BEQ	sc_b3
	CMP	$0x7d, R6
	BEQ	sc_b3
	CMP	$0x5b, R6
	BEQ	sc_b3
	CMP	$0x5d, R6
	BEQ	sc_b3
	ADD	$4, R3, R3
	B	sc_bulk

sc_b1:
	ADD	$1, R3, R3
	B	sc_loop
sc_b2:
	ADD	$2, R3, R3
	B	sc_loop
sc_b3:
	ADD	$3, R3, R3
	B	sc_loop

sc_open:
	ADD	$1, R5, R5
	ADD	$1, R3, R3
	CMP	R4, R3
	BHS	sc_fail
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	sc_string
	CMP	$0x7b, R6
	BEQ	sc_open
	CMP	$0x5b, R6
	BEQ	sc_open
	B	sc_dispatch

sc_close:
	SUBS	$1, R5, R5
	ADD	$1, R3, R3
	BEQ	sc_done
	CMP	R4, R3
	BHS	sc_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	sc_sep
	CMP	$0x22, R6
	BEQ	sc_string
	CMP	$0x7d, R6
	BEQ	sc_close
	CMP	$0x5d, R6
	BEQ	sc_close
	B	sc_dispatch

sc_string:
	ADD	$1, R3, R3
	MOVD	R3, R2
	MOVD	$4, R15

sc_str_swar:
	SUB	R3, R4, R7
	CMP	$8, R7
	BLT	sc_str_scalar
	MOVD	(R3), R8
	EOR	R10, R8, R6
	SUB	R12, R6, R7
	MVN	R6, R9
	AND	R9, R7, R7
	AND	R13, R7, R6
	CBNZ	R6, sc_str_qhit
	ADD	$8, R3, R3
	SUBS	$1, R15, R15
	BNE	sc_str_swar
	B	sc_str_neon

sc_str_qhit:
	TST	$0x80, R6
	BNE	sc_str_p0
	TST	$0x8000, R6
	BNE	sc_str_p1
	TST	$0x800000, R6
	BNE	sc_str_p2
	TST	$0x80000000, R6
	BNE	sc_str_p3
	TST	$0x8000000000, R6
	BNE	sc_str_p4
	TST	$0x800000000000, R6
	BNE	sc_str_p5
	TST	$0x80000000000000, R6
	BNE	sc_str_p6
	ADD	$7, R3, R3
	B	sc_str_atq

sc_str_p0:
	B	sc_str_atq
sc_str_p1:
	MOVBU	(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos1
	ADD	$2, R3, R3
	B	sc_after_string
sc_str_escpos1:
	ADD	$1, R3, R3
	B	sc_str_atq
sc_str_p2:
	MOVBU	1(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos2
	ADD	$3, R3, R3
	B	sc_after_string
sc_str_escpos2:
	ADD	$2, R3, R3
	B	sc_str_atq
sc_str_p3:
	MOVBU	2(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos3
	ADD	$4, R3, R3
	B	sc_after_string
sc_str_escpos3:
	ADD	$3, R3, R3
	B	sc_str_atq
sc_str_p4:
	MOVBU	3(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos4
	ADD	$5, R3, R3
	B	sc_after_string
sc_str_escpos4:
	ADD	$4, R3, R3
	B	sc_str_atq
sc_str_p5:
	MOVBU	4(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos5
	ADD	$6, R3, R3
	B	sc_after_string
sc_str_escpos5:
	ADD	$5, R3, R3
	B	sc_str_atq
sc_str_p6:
	MOVBU	5(R3), R9
	CMP	$0x5c, R9
	BEQ	sc_str_escpos6
	ADD	$7, R3, R3
	B	sc_after_string
sc_str_escpos6:
	ADD	$6, R3, R3
	B	sc_str_atq

sc_str_atq:
	CMP	R2, R3
	BEQ	sc_str_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	sc_str_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
sc_str_bscount:
	CMP	R2, R14
	BLS	sc_str_bsdone
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	sc_str_bsdone
	ADD	$1, R7, R7
	B	sc_str_bscount
sc_str_bsdone:
	TST	$1, R7
	BEQ	sc_str_unesc
	ADD	$1, R3, R3
	MOVD	$4, R15
	B	sc_str_swar

sc_str_unesc:
	ADD	$1, R3, R3
	B	sc_after_string

sc_str_neon:
	SUB	R3, R4, R7
	CMP	$16, R7
	BLT	sc_str_more
	PRFM	128(R3), PLDL1KEEP
	VLD1	(R3), [V2.B16]
	VCMEQ	V0.B16, V2.B16, V3.B16
	VADDP	V3.D2, V3.D2, V5.D2
	VMOV	V5.D[0], R8
	CBNZ	R8, sc_str_neon_hit
	ADD	$16, R3, R3
	B	sc_str_neon

sc_str_neon_hit:
	VMOV	V3.D[0], R6
	CBNZ	R6, sc_str_neon_lo
	ADD	$8, R3, R3
	VMOV	V3.D[1], R6
sc_str_neon_lo:
	RBIT	R6, R8
	CLZ	R8, R8
	LSR	$3, R8, R8
	ADD	R8, R3, R3
	B	sc_str_atq

sc_str_more:
	MOVD	$4, R15
	B	sc_str_swar

sc_str_scalar:
	SUB	R3, R4, R7
	CBZ	R7, sc_fail
sc_str_scalar_loop:
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	sc_str_scal_q
	CMP	$0x5c, R6
	BEQ	sc_str_esc
	ADD	$1, R3, R3
	SUBS	$1, R7, R7
	BNE	sc_str_scalar_loop
	B	sc_fail

sc_str_scal_q:
	CMP	R2, R3
	BEQ	sc_str_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	sc_str_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
sc_str_scal_bsc:
	CMP	R2, R14
	BLS	sc_str_scal_bsd
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	sc_str_scal_bsd
	ADD	$1, R7, R7
	B	sc_str_scal_bsc
sc_str_scal_bsd:
	TST	$1, R7
	BEQ	sc_str_unesc
	ADD	$1, R3, R3
	SUB	R3, R4, R7
	B	sc_str_scalar_loop

sc_str_esc:
	ADD	$2, R3, R3
	CMP	R4, R3
	BHI	sc_fail
	SUB	R3, R4, R7
	B	sc_str_scalar_loop

sc_after_string:
	CMP	R4, R3
	BHS	sc_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	sc_sep
	CMP	$0x3a, R6
	BEQ	sc_sep
	CMP	$0x7d, R6
	BEQ	sc_close
	CMP	$0x5d, R6
	BEQ	sc_close
	CMP	$0x22, R6
	BEQ	sc_string
	B	sc_dispatch

sc_sep:
	ADD	$1, R3, R3
	CMP	R4, R3
	BHS	sc_fail
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	sc_string
	CMP	$0x7b, R6
	BEQ	sc_open
	CMP	$0x5b, R6
	BEQ	sc_open
	B	sc_dispatch

sc_done:
	SUB	R0, R3, R0
	MOVD	R0, ret+32(FP)
	MOVW	$1, R9
	MOVB	R9, ret1+40(FP)
	RET

sc_fail:
	MOVD	data_len+8(FP), R0
	MOVD	R0, ret+32(FP)
	MOVB	ZR, ret1+40(FP)
	RET
