//go:build amd64 && !purego

#include "textflag.h"

// func indexQuoteOrBackslashAVX2(data []byte, i int) int
// 32-byte AVX2 scan for '"' or '\\'. Falls through to SSE/scalar for tails.
TEXT ·indexQuoteOrBackslashAVX2(SB), NOSPLIT, $0-40
	MOVQ	data_base+0(FP), SI
	MOVQ	data_len+8(FP), BX
	MOVQ	i+24(FP), DX
	CMPQ	DX, BX
	JAE	fail

	ADDQ	DX, SI
	SUBQ	DX, BX
	MOVQ	SI, DI

	// broadcast '"' into Y0, '\\' into Y1
	MOVQ	$0x22, AX
	MOVQ	AX, X0
	VPBROADCASTB	X0, Y0
	MOVQ	$0x5c, AX
	MOVQ	AX, X1
	VPBROADCASTB	X1, Y1

	CMPQ	BX, $32
	JB	sse

avx_loop:
	VMOVDQU	(DI), Y2
	VPCMPEQB	Y0, Y2, Y3
	VPCMPEQB	Y1, Y2, Y4
	VPOR	Y4, Y3, Y3
	VPMOVMSKB	Y3, AX
	TESTL	AX, AX
	JNZ	avx_hit
	ADDQ	$32, DI
	SUBQ	$32, BX
	CMPQ	BX, $32
	JAE	avx_loop

	VZEROUPPER

sse:
	CMPQ	BX, $16
	JB	tail
	// broadcast into X0/X1 for SSE path
	MOVQ	$0x22, AX
	MOVQ	AX, X0
	PUNPCKLBW	X0, X0
	PUNPCKLBW	X0, X0
	PSHUFL	$0, X0, X0
	MOVQ	$0x5c, AX
	MOVQ	AX, X1
	PUNPCKLBW	X1, X1
	PUNPCKLBW	X1, X1
	PSHUFL	$0, X1, X1

sse_loop:
	MOVOU	(DI), X2
	MOVO	X2, X3
	PCMPEQB	X0, X2
	PCMPEQB	X1, X3
	POR	X3, X2
	PMOVMSKB	X2, AX
	TESTL	AX, AX
	JNZ	sse_hit
	ADDQ	$16, DI
	SUBQ	$16, BX
	CMPQ	BX, $16
	JAE	sse_loop

tail:
	TESTQ	BX, BX
	JZ	fail
tail_loop:
	MOVBLZX	(DI), AX
	CMPB	AX, $0x22
	JE	hit_byte
	CMPB	AX, $0x5c
	JE	hit_byte
	ADDQ	$1, DI
	SUBQ	$1, BX
	JNZ	tail_loop
	JMP	fail

avx_hit:
	BSFL	AX, AX
	ADDQ	AX, DI
	JMP	hit_ptr

sse_hit:
	BSFL	AX, AX
	ADDQ	AX, DI
	JMP	hit_ptr

hit_byte:
hit_ptr:
	MOVQ	data_base+0(FP), AX
	SUBQ	AX, DI
	MOVQ	DI, ret+32(FP)
	RET

fail:
	MOVQ	$-1, ret+32(FP)
	RET

// func skipStringBodyAVX2(data []byte, i int) (int, bool)
TEXT ·skipStringBodyAVX2(SB), NOSPLIT, $0-49
	MOVQ	data_base+0(FP), SI
	MOVQ	data_len+8(FP), BX
	MOVQ	i+24(FP), DX
	CMPQ	DX, BX
	JAE	ss_fail

	ADDQ	DX, SI
	SUBQ	DX, BX
	MOVQ	SI, DI

	MOVQ	$0x22, AX
	MOVQ	AX, X0
	VPBROADCASTB	X0, Y0
	MOVQ	$0x5c, AX
	MOVQ	AX, X1
	VPBROADCASTB	X1, Y1

ss_avx:
	CMPQ	BX, $32
	JB	ss_sse
	VMOVDQU	(DI), Y2
	VPCMPEQB	Y0, Y2, Y3
	VPCMPEQB	Y1, Y2, Y4
	VPOR	Y4, Y3, Y3
	VPMOVMSKB	Y3, AX
	TESTL	AX, AX
	JNZ	ss_avx_hit
	ADDQ	$32, DI
	SUBQ	$32, BX
	JMP	ss_avx

ss_avx_hit:
	VZEROUPPER
	BSFL	AX, CX
	// scan from DI for CX+1 bytes looking carefully for escapes order
	// Actually mask may have multiple hits; BSF gives first. Check that byte.
	MOVBLZX	(DI)(CX*1), AX
	CMPB	AX, $0x22
	JE	ss_quote_cx
	// backslash at CX
	// need CX+2 available
	LEAQ	2(CX), AX
	CMPQ	AX, BX
	JA	ss_fail
	ADDQ	AX, DI
	SUBQ	AX, BX
	// rebroadcast - Y registers may still be ok after VZEROUPPER need reload
	MOVQ	$0x22, AX
	MOVQ	AX, X0
	VPBROADCASTB	X0, Y0
	MOVQ	$0x5c, AX
	MOVQ	AX, X1
	VPBROADCASTB	X1, Y1
	JMP	ss_avx

ss_quote_cx:
	VZEROUPPER
	LEAQ	1(DI)(CX*1), DI
	MOVQ	data_base+0(FP), AX
	SUBQ	AX, DI
	MOVQ	DI, ret+32(FP)
	MOVB	$1, ret+40(FP)
	RET

ss_sse:
	VZEROUPPER
	CMPQ	BX, $16
	JB	ss_tail
	MOVQ	$0x22, AX
	MOVQ	AX, X0
	PUNPCKLBW	X0, X0
	PUNPCKLBW	X0, X0
	PSHUFL	$0, X0, X0
	MOVQ	$0x5c, AX
	MOVQ	AX, X1
	PUNPCKLBW	X1, X1
	PUNPCKLBW	X1, X1
	PSHUFL	$0, X1, X1

ss_sse_loop:
	MOVOU	(DI), X2
	MOVO	X2, X3
	PCMPEQB	X0, X2
	PCMPEQB	X1, X3
	POR	X3, X2
	PMOVMSKB	X2, AX
	TESTL	AX, AX
	JNZ	ss_sse_hit
	ADDQ	$16, DI
	SUBQ	$16, BX
	CMPQ	BX, $16
	JAE	ss_sse_loop
	JMP	ss_tail

ss_sse_hit:
	BSFL	AX, CX
	MOVBLZX	(DI)(CX*1), AX
	CMPB	AX, $0x22
	JE	ss_quote_cx_sse
	LEAQ	2(CX), AX
	CMPQ	AX, BX
	JA	ss_fail
	ADDQ	AX, DI
	SUBQ	AX, BX
	JMP	ss_sse

ss_quote_cx_sse:
	LEAQ	1(DI)(CX*1), DI
	MOVQ	data_base+0(FP), AX
	SUBQ	AX, DI
	MOVQ	DI, ret+32(FP)
	MOVB	$1, ret+40(FP)
	RET

ss_tail:
	TESTQ	BX, BX
	JZ	ss_fail
ss_tail_loop:
	MOVBLZX	(DI), AX
	CMPB	AX, $0x22
	JE	ss_quote_byte
	CMPB	AX, $0x5c
	JE	ss_esc_byte
	ADDQ	$1, DI
	SUBQ	$1, BX
	JNZ	ss_tail_loop
	JMP	ss_fail

ss_esc_byte:
	CMPQ	BX, $2
	JB	ss_fail
	ADDQ	$2, DI
	SUBQ	$2, BX
	JMP	ss_tail

ss_quote_byte:
	ADDQ	$1, DI
	MOVQ	data_base+0(FP), AX
	SUBQ	AX, DI
	MOVQ	DI, ret+32(FP)
	MOVB	$1, ret+40(FP)
	RET

ss_fail:
	MOVQ	data_len+8(FP), AX
	MOVQ	AX, ret+32(FP)
	MOVB	$0, ret+40(FP)
	RET
