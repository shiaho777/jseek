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
TEXT ·skipStringBodyNEON(SB), NOSPLIT, $0-49
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
	MOVB	R9, ret+40(FP)
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
	MOVB	ZR, ret+40(FP)
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
TEXT ·skipContainerNEON(SB), NOSPLIT, $0-49
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
	MOVB	R9, ret+40(FP)
	RET

sc_fail:
	MOVD	data_len+8(FP), R0
	MOVD	R0, ret+32(FP)
	MOVB	ZR, ret+40(FP)
	RET

// func findIndexNEON(data []byte, ai int, n int) (int, bool)
TEXT ·findIndexNEON(SB), NOSPLIT, $0-49
	MOVD	data_base+0(FP), R0
	MOVD	data_len+8(FP), R1
	MOVD	ai+24(FP), R2
	MOVD	n+32(FP), R20

	CMP	R1, R2
	BHS	fi_fail
	ADD	R2, R0, R3
	ADD	$1, R3, R3
	ADD	R1, R0, R4

	MOVD	$0x2222222222222222, R10
	MOVD	$0x0101010101010101, R12
	MOVD	$0x8080808080808080, R13
	MOVD	$0x22, R6
	VMOV	R6, V0.B16

fi_ws0:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x20, R6
	BEQ	fi_ws0a
	CMP	$0x09, R6
	BEQ	fi_ws0a
	CMP	$0x0a, R6
	BEQ	fi_ws0a
	CMP	$0x0d, R6
	BEQ	fi_ws0a
	B	fi_ws0d
fi_ws0a:
	ADD	$1, R3, R3
	B	fi_ws0
fi_ws0d:
	CMP	$0x5d, R6
	BEQ	fi_fail

fi_loop:
	CBZ	R20, fi_found
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x7b, R6
	BEQ	fi_obj
	CMP	$0x5b, R6
	BEQ	fi_obj
	CMP	$0x22, R6
	BEQ	fi_str
	B	fi_scalar

fi_obj:
	ADD	$1, R3, R3
	MOVD	$1, R5

fi_sc_loop:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
fi_sc_dispatch:
	CMP	$0x22, R6
	BEQ	fi_sc_string
	CMP	$0x7b, R6
	BEQ	fi_sc_open
	CMP	$0x7d, R6
	BEQ	fi_sc_close
	CMP	$0x5b, R6
	BEQ	fi_sc_open
	CMP	$0x5d, R6
	BEQ	fi_sc_close
	ADD	$1, R3, R3

fi_sc_bulk:
	ADD	$4, R3, R7
	CMP	R4, R7
	BHI	fi_sc_loop
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_loop
	CMP	$0x7b, R6
	BEQ	fi_sc_loop
	CMP	$0x7d, R6
	BEQ	fi_sc_loop
	CMP	$0x5b, R6
	BEQ	fi_sc_loop
	CMP	$0x5d, R6
	BEQ	fi_sc_loop
	MOVBU	1(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_b1
	CMP	$0x7b, R6
	BEQ	fi_sc_b1
	CMP	$0x7d, R6
	BEQ	fi_sc_b1
	CMP	$0x5b, R6
	BEQ	fi_sc_b1
	CMP	$0x5d, R6
	BEQ	fi_sc_b1
	MOVBU	2(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_b2
	CMP	$0x7b, R6
	BEQ	fi_sc_b2
	CMP	$0x7d, R6
	BEQ	fi_sc_b2
	CMP	$0x5b, R6
	BEQ	fi_sc_b2
	CMP	$0x5d, R6
	BEQ	fi_sc_b2
	MOVBU	3(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_b3
	CMP	$0x7b, R6
	BEQ	fi_sc_b3
	CMP	$0x7d, R6
	BEQ	fi_sc_b3
	CMP	$0x5b, R6
	BEQ	fi_sc_b3
	CMP	$0x5d, R6
	BEQ	fi_sc_b3
	ADD	$4, R3, R3
	B	fi_sc_bulk
fi_sc_b1:
	ADD	$1, R3, R3
	B	fi_sc_loop
fi_sc_b2:
	ADD	$2, R3, R3
	B	fi_sc_loop
fi_sc_b3:
	ADD	$3, R3, R3
	B	fi_sc_loop

fi_sc_open:
	ADD	$1, R5, R5
	ADD	$1, R3, R3
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_string
	CMP	$0x7b, R6
	BEQ	fi_sc_open
	CMP	$0x5b, R6
	BEQ	fi_sc_open
	B	fi_sc_dispatch

fi_sc_close:
	SUBS	$1, R5, R5
	ADD	$1, R3, R3
	BEQ	fi_after_tight
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	fi_sc_sep
	CMP	$0x22, R6
	BEQ	fi_sc_string
	CMP	$0x7d, R6
	BEQ	fi_sc_close
	CMP	$0x5d, R6
	BEQ	fi_sc_close
	B	fi_sc_dispatch

fi_after_tight:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	fi_comma_tight
	B	fi_after

fi_comma_tight:
	ADD	$1, R3, R3
	SUBS	$1, R20, R20
	BEQ	fi_found
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x7b, R6
	BEQ	fi_obj
	CMP	$0x5b, R6
	BEQ	fi_obj
	CMP	$0x22, R6
	BEQ	fi_str
	B	fi_scalar

fi_sc_string:
	ADD	$1, R3, R3
	MOVD	R3, R2
	MOVD	$4, R15
fi_sc_swar:
	SUB	R3, R4, R7
	CMP	$8, R7
	BLT	fi_sc_sclr
	MOVD	(R3), R8
	EOR	R10, R8, R6
	SUB	R12, R6, R7
	MVN	R6, R9
	AND	R9, R7, R7
	AND	R13, R7, R6
	CBNZ	R6, fi_sc_qhit
	ADD	$8, R3, R3
	SUBS	$1, R15, R15
	BNE	fi_sc_swar
	B	fi_sc_neon
fi_sc_qhit:
	TST	$0x80, R6
	BNE	fi_sc_p0
	TST	$0x8000, R6
	BNE	fi_sc_p1
	TST	$0x800000, R6
	BNE	fi_sc_p2
	TST	$0x80000000, R6
	BNE	fi_sc_p3
	TST	$0x8000000000, R6
	BNE	fi_sc_p4
	TST	$0x800000000000, R6
	BNE	fi_sc_p5
	TST	$0x80000000000000, R6
	BNE	fi_sc_p6
	ADD	$7, R3, R3
	B	fi_sc_atq
fi_sc_p0:
	B	fi_sc_atq
fi_sc_p1:
	MOVBU	(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep1
	ADD	$2, R3, R3
	B	fi_sc_astr
fi_sc_ep1:
	ADD	$1, R3, R3
	B	fi_sc_atq
fi_sc_p2:
	MOVBU	1(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep2
	ADD	$3, R3, R3
	B	fi_sc_astr
fi_sc_ep2:
	ADD	$2, R3, R3
	B	fi_sc_atq
fi_sc_p3:
	MOVBU	2(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep3
	ADD	$4, R3, R3
	B	fi_sc_astr
fi_sc_ep3:
	ADD	$3, R3, R3
	B	fi_sc_atq
fi_sc_p4:
	MOVBU	3(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep4
	ADD	$5, R3, R3
	B	fi_sc_astr
fi_sc_ep4:
	ADD	$4, R3, R3
	B	fi_sc_atq
fi_sc_p5:
	MOVBU	4(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep5
	ADD	$6, R3, R3
	B	fi_sc_astr
fi_sc_ep5:
	ADD	$5, R3, R3
	B	fi_sc_atq
fi_sc_p6:
	MOVBU	5(R3), R9
	CMP	$0x5c, R9
	BEQ	fi_sc_ep6
	ADD	$7, R3, R3
	B	fi_sc_astr
fi_sc_ep6:
	ADD	$6, R3, R3
	B	fi_sc_atq
fi_sc_atq:
	CMP	R2, R3
	BEQ	fi_sc_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	fi_sc_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
fi_sc_bsc:
	CMP	R2, R14
	BLS	fi_sc_bsd
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	fi_sc_bsd
	ADD	$1, R7, R7
	B	fi_sc_bsc
fi_sc_bsd:
	TST	$1, R7
	BEQ	fi_sc_unesc
	ADD	$1, R3, R3
	MOVD	$4, R15
	B	fi_sc_swar
fi_sc_unesc:
	ADD	$1, R3, R3
	B	fi_sc_astr
fi_sc_neon:
	SUB	R3, R4, R7
	CMP	$16, R7
	BLT	fi_sc_more
	PRFM	128(R3), PLDL1KEEP
	VLD1	(R3), [V2.B16]
	VCMEQ	V0.B16, V2.B16, V3.B16
	VADDP	V3.D2, V3.D2, V5.D2
	VMOV	V5.D[0], R8
	CBNZ	R8, fi_sc_nhit
	ADD	$16, R3, R3
	B	fi_sc_neon
fi_sc_nhit:
	VMOV	V3.D[0], R6
	CBNZ	R6, fi_sc_nlo
	ADD	$8, R3, R3
	VMOV	V3.D[1], R6
fi_sc_nlo:
	RBIT	R6, R8
	CLZ	R8, R8
	LSR	$3, R8, R8
	ADD	R8, R3, R3
	B	fi_sc_atq
fi_sc_more:
	MOVD	$4, R15
	B	fi_sc_swar
fi_sc_sclr:
	SUB	R3, R4, R7
	CBZ	R7, fi_fail
fi_sc_sclr_loop:
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_sclr_q
	CMP	$0x5c, R6
	BEQ	fi_sc_sclr_e
	ADD	$1, R3, R3
	SUBS	$1, R7, R7
	BNE	fi_sc_sclr_loop
	B	fi_fail
fi_sc_sclr_q:
	CMP	R2, R3
	BEQ	fi_sc_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	fi_sc_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
fi_sc_sclr_bsc:
	CMP	R2, R14
	BLS	fi_sc_sclr_bsd
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	fi_sc_sclr_bsd
	ADD	$1, R7, R7
	B	fi_sc_sclr_bsc
fi_sc_sclr_bsd:
	TST	$1, R7
	BEQ	fi_sc_unesc
	ADD	$1, R3, R3
	SUB	R3, R4, R7
	B	fi_sc_sclr_loop
fi_sc_sclr_e:
	ADD	$2, R3, R3
	CMP	R4, R3
	BHI	fi_fail
	SUB	R3, R4, R7
	B	fi_sc_sclr_loop

fi_sc_astr:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	fi_sc_sep
	CMP	$0x3a, R6
	BEQ	fi_sc_sep
	CMP	$0x7d, R6
	BEQ	fi_sc_close
	CMP	$0x5d, R6
	BEQ	fi_sc_close
	CMP	$0x22, R6
	BEQ	fi_sc_string
	B	fi_sc_dispatch

fi_sc_sep:
	ADD	$1, R3, R3
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	fi_sc_string
	CMP	$0x7b, R6
	BEQ	fi_sc_open
	CMP	$0x5b, R6
	BEQ	fi_sc_open
	B	fi_sc_dispatch

fi_str:
	ADD	$1, R3, R3
	MOVD	R3, R2
	MOVD	$4, R15
fi_str_swar:
	SUB	R3, R4, R7
	CMP	$8, R7
	BLT	fi_str_sclr
	MOVD	(R3), R8
	EOR	R10, R8, R6
	SUB	R12, R6, R7
	MVN	R6, R9
	AND	R9, R7, R7
	AND	R13, R7, R6
	CBNZ	R6, fi_str_qhit
	ADD	$8, R3, R3
	SUBS	$1, R15, R15
	BNE	fi_str_swar
	B	fi_str_sclr
fi_str_qhit:
	RBIT	R6, R8
	CLZ	R8, R8
	LSR	$3, R8, R8
	ADD	R8, R3, R3
	CMP	R2, R3
	BEQ	fi_str_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	fi_str_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
fi_str_bsc:
	CMP	R2, R14
	BLS	fi_str_bsd
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	fi_str_bsd
	ADD	$1, R7, R7
	B	fi_str_bsc
fi_str_bsd:
	TST	$1, R7
	BEQ	fi_str_unesc
	ADD	$1, R3, R3
	MOVD	$4, R15
	B	fi_str_swar
fi_str_unesc:
	ADD	$1, R3, R3
	B	fi_after
fi_str_sclr:
	SUB	R3, R4, R7
	CBZ	R7, fi_fail
fi_str_sclr_loop:
	MOVBU	(R3), R6
	CMP	$0x22, R6
	BEQ	fi_str_sclr_q
	CMP	$0x5c, R6
	BEQ	fi_str_sclr_e
	ADD	$1, R3, R3
	SUBS	$1, R7, R7
	BNE	fi_str_sclr_loop
	B	fi_fail
fi_str_sclr_q:
	CMP	R2, R3
	BEQ	fi_str_unesc
	MOVBU	-1(R3), R9
	CMP	$0x5c, R9
	BNE	fi_str_unesc
	MOVD	ZR, R7
	MOVD	R3, R14
fi_str_sclr_bsc2:
	CMP	R2, R14
	BLS	fi_str_sclr_bsd2
	SUB	$1, R14, R14
	MOVBU	(R14), R9
	CMP	$0x5c, R9
	BNE	fi_str_sclr_bsd2
	ADD	$1, R7, R7
	B	fi_str_sclr_bsc2
fi_str_sclr_bsd2:
	TST	$1, R7
	BEQ	fi_str_unesc
	ADD	$1, R3, R3
	SUB	R3, R4, R7
	B	fi_str_sclr_loop
fi_str_sclr_e:
	ADD	$2, R3, R3
	CMP	R4, R3
	BHI	fi_fail
	SUB	R3, R4, R7
	B	fi_str_sclr_loop

fi_scalar:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x2c, R6
	BEQ	fi_after
	CMP	$0x5d, R6
	BEQ	fi_after
	CMP	$0x20, R6
	BEQ	fi_after
	CMP	$0x09, R6
	BEQ	fi_after
	CMP	$0x0a, R6
	BEQ	fi_after
	CMP	$0x0d, R6
	BEQ	fi_after
	ADD	$1, R3, R3
	B	fi_scalar

fi_after:
fi_ws1:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x20, R6
	BEQ	fi_ws1a
	CMP	$0x09, R6
	BEQ	fi_ws1a
	CMP	$0x0a, R6
	BEQ	fi_ws1a
	CMP	$0x0d, R6
	BEQ	fi_ws1a
	B	fi_ws1d
fi_ws1a:
	ADD	$1, R3, R3
	B	fi_ws1
fi_ws1d:
	CMP	$0x2c, R6
	BEQ	fi_comma
	B	fi_fail
fi_comma:
	ADD	$1, R3, R3
	SUB	$1, R20, R20
fi_ws2:
	CMP	R4, R3
	BHS	fi_fail
	MOVBU	(R3), R6
	CMP	$0x20, R6
	BEQ	fi_ws2a
	CMP	$0x09, R6
	BEQ	fi_ws2a
	CMP	$0x0a, R6
	BEQ	fi_ws2a
	CMP	$0x0d, R6
	BEQ	fi_ws2a
	B	fi_loop
fi_ws2a:
	ADD	$1, R3, R3
	B	fi_ws2

fi_found:
	SUB	R0, R3, R0
	MOVD	R0, ret+40(FP)
	MOVW	$1, R9
	MOVB	R9, ret+48(FP)
	RET

fi_fail:
	MOVD	data_len+8(FP), R0
	MOVD	R0, ret+40(FP)
	MOVB	ZR, ret+48(FP)
	RET
