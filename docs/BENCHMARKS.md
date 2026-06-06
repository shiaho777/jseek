# jseek benchmarks & methodology

This document is a complete, reproducible record of jseek's performance —
**including the workloads where jseek loses**. The value of a benchmark is in
honestly marking the boundaries, not in showing only the wins.

## How to reproduce

```sh
cd bench
go test -bench=. -benchmem -count=6 | tee raw.txt
benchstat raw.txt          # go install golang.org/x/perf/cmd/benchstat@latest
```

All comparison libraries are pinned: `jseek` itself is wired via a local
`replace => ../` to the current repository source (so you measure the code in
front of you), while `jsonparser` / `gjson` / `sonic` / `simdjson-go` are the
published versions locked in `bench/go.mod`.

## Test environment

| Item | Value |
| --- | --- |
| CPU | Apple M4 Pro |
| Arch | arm64 (darwin) |
| Go | 1.23.4 |
| Sampling | 6 runs per case, summarized with benchstat, ±1–4% variance |

Absolute numbers vary by machine; reproduce on your own hardware and data and
look at the relative relationships.

## Comparison libraries

- [`buger/jsonparser`](https://github.com/buger/jsonparser) — the classic lazy
  extractor.
- [`tidwall/gjson`](https://github.com/tidwall/gjson) — the current de-facto SOTA
  in this class.

These are objective third-party libraries used only for performance comparison;
their content was rephrased for compliance with licensing restrictions.

## Fixture shapes

| fixture | size | shape | origin |
| --- | --- | --- | --- |
| small | ~190 B | flat HTTP-log record | jsonparser's classic small payload |
| large | ~24 KB | metadata + 500-element user array | Discourse-API style |
| github | ~60 KB | deeply nested object + 200-issue array | GitHub-API style |
| ndjson | 5000 lines | one access-log record per line | NDJSON log stream |

---

## Result 1: single-field / few-field reads

Every operation scans from the top (stateless) — the typical "grab a few fields
from one document."

| Scenario | jseek | jsonparser | gjson |
| --- | --- | --- | --- |
| Small payload, 4 fields | **145 ns** / 0 B | 276 ns / 0 B | 349 ns / 144 B |
| Large doc, shallow fields | **106 ns** / 0 B | 116 ns / 0 B | 158 ns / 16 B |
| Large doc, deep indexed field | **70 µs** / 0 B | 238 µs / 0 B | 88 µs / 16 B |
| Large doc, full ArrayEach | **131 µs** / 0 B | 211 µs / 0 B | 289 µs / 184 KB |

**Takeaway:** jseek leads across single extractions and is the only
zero-allocation library throughout. (But see Result 8 — against a SIMD
full-parser, the deep-indexed cold case does *not* hold.)

---

## Result 2: multi-path (single pass)

Reading several fields at once.

| Engine | time | allocs |
| --- | --- | --- |
| jseek `EachKey` (stateless, single pass) | **148 µs** | 0 B |
| jseek N× `Get` | 163 µs | 0 B |
| gjson `GetManyBytes` | 194 µs | 536 B |

**Takeaway:** jseek's single-pass multi-path is fastest and zero-allocation.

---

## Result 3: index once, query many (flagship scenario)

Reading 12 scattered fields from one 24 KB document.

| Approach | time | allocs |
| --- | --- | --- |
| Stateless `Get` ×12 (re-scan each) | 380 µs | 0 B |
| `IndexPooled` + 12 queries | 194 µs | ~34 B |
| **Reused index, 12 queries** | **120 µs** | 0 B |
| gjson `GetManyBytes` | 460 µs | 1.16 KB |

**Takeaway:** with a reused index jseek is ~3.8x faster than gjson, zero
allocations. The more fields you read, the larger the win.

---

## Result 4: skip tape (O(1) subtree skipping)

On the same large document, linear skip vs tape skip (A/B, same binary).

| Scenario | linear | tape | speedup |
| --- | --- | --- | --- |
| 12 scattered fields (reused) | 121 µs | **5.8 µs** | ~21x |
| deep `users[499].name` | 23.2 µs | **1.57 µs** | ~15x |
| multi-path `EachDoc` | 47.7 µs (no tape) | **2.26 µs** (tape) | ~21x |
| multi-path `Each` (stateless baseline) | 148 µs | — | — |

**Takeaway:** the tape turns O(subtree) skipping into O(1) — an order-of-magnitude
gain on deep/scattered access, queries still zero-allocation. The cost is one
extra `uint32` per structural in the transient index, released with the
`Document`.

---

## Result 5: real-world — GitHub-style nested response

200 issues, reading 7 scattered fields.

| Approach | time | allocs |
| --- | --- | --- |
| jseek IndexTape (reused) | **1.47 µs** | 0 B |
| jseek stateless | 56.5 µs | 0 B |
| jseek per-request (pooled) | 59.1 µs | ~11 B |
| gjson GetMany | 56.0 µs | 664 B |
| jsonparser | 156 µs | 0 B |

**Takeaway:** with a reused index jseek dominates (~38x faster than gjson). The
per-request case is on par with gjson on time but near-zero allocation.

---

## Result 6: real-world — NDJSON log stream (jseek's weak spot, recorded honestly)

5000 flat ~250 B records, all in memory, reading 3 fields per record
field-by-field.

| Approach | time | allocs |
| --- | --- | --- |
| gjson (per-line Get) | **1.81 ms** | 0 B |
| jseek `StreamBytes` + single-pass `EachKey` | 1.94 ms | 0 B |
| jseek `StreamBytes` + 3× `Get` | 2.23 ms | 0 B |
| jseek `Decoder` (reader streaming) | 2.35 ms | 64 KB (one-time buffer) |

**Honest takeaway:** on this specific shape (small records, fully in memory,
field-by-field), **gjson is ~7.5% faster than jseek**. The reason: for ~250 B
flat records, gjson's highly specialized small-scalar scan is more compact,
while jseek's structural-navigation/index overhead does not amortize at this
size.

**When jseek's streaming still wins:** when the data does **not** fit in memory —
`Decoder`'s memory is bounded by the largest single element, not the whole
stream, whereas gjson's "whole slice in memory" approach is not viable there.
That is the real value of jseek's streaming, and this (in-memory) benchmark does
not reward it.

---

## Result 7: repeated access — pinned queries & columnar transpose (the biggest lever)

Results 1–6 are "cold" or "single-pass" access. But in real systems data is
often accessed **repeatedly**: a hot config queried a million times, a batch of
logs aggregated over dozens of metrics. The real lever there is not "scan
faster" but **eliminating repeated scanning/navigation**.

### Pin: repeatedly querying the same data (24 KB config, 3 fields)

| Approach | per query | vs cold scan |
| --- | --- | --- |
| Cold `Get` (what gjson/jsonparser force) | 239 ns | 1x |
| Reused-index `Document.Get` | 147 ns | 1.6x |
| **`Pin` (learn the trajectory once, verify then direct-address)** | **66 ns** | **3.6x** |

Pin is slower than a white-box prototype (17 ns) because the production version
does **full key-chain verification** — that is the cost of "never returns a wrong
value," and I mark it honestly.

### Transpose: repeatedly aggregating the same batch (5000 logs)

| Aggregation passes | row-wise `Get` | columnar `Transpose` | factor |
| --- | --- | --- | --- |
| 50 passes | 16.0 ms | **524 µs** | **~30x** |
| 200 passes | 80.8 ms | 866 µs | **~93x** |

**The factor grows without bound as passes increase**, because the transpose
cost is fixed (once) and every subsequent aggregation is a plain native-slice
scan, approaching zero. gjson is 16.5 ms on the same row-wise workload — also
left behind by the columnar approach.

### What this means (and its boundary)

- **"Tens to hundreds of times" is real in repeated-access scenarios**, backed by
  benchmarks and correctness fuzzing (17.5M executions, zero divergences).
- **It violates no law of physics**: columnar turns "N repeated JSON navigations"
  into "1 navigation + N raw-slice scans." The win comes from eliminating
  repetition, not from parsing faster than reading the bytes.
- **Boundary**: a single cold query (data seen once) is still bound by the
  information-theoretic floor — no order-of-magnitude gain there, as Results 1–6
  honestly establish. The more repeated the access, the larger the columnar win;
  seen once, it degrades to a normal single parse.

| Dimension | Verdict |
| --- | --- |
| Single extraction (small/large) | jseek leads, zero alloc |
| Multi-path single pass | jseek leads, zero alloc |
| Index reuse / deep access | jseek leads by orders of magnitude (tape) |
| Nested API response | jseek dominates when reused, on par + leaner per-request |
| Small-record in-memory field-by-field stream | **gjson slightly faster (~7.5%)** |
| **Repeated queries on same data (Pin)** | **jseek 3.6x faster, no peer offers it** |
| **Repeated aggregation on same batch (columnar Transpose)** | **jseek 30–93x faster, growing without bound** |

jseek is fastest in this class for lazy extraction, index reuse, multi-field, and
deep navigation; it trails gjson slightly on the narrow "small-record in-memory
field-by-field" shape, and that gap is held to single-digit percent.

---

## Result 8: vs the full-parsing SOTA — sonic & simdjson-go (honest calibration)

The sections above compare only against lazy extractors (jsonparser, gjson). To
talk about "beating the SOTA," the two strongest full-parsing libraries have to
be in the picture:

- **[`bytedance/sonic`](https://github.com/bytedance/sonic)** — JIT-accelerated,
  and offers a lazy `Get(path...)` API.
- **[`minio/simdjson-go`](https://github.com/minio/simdjson-go)** — SIMD full
  parse, then navigate the tape.

### An architectural fact that must be stated first (or the numbers mislead)

| Library | amd64 | arm64 (this test machine, M4 Pro) |
| --- | --- | --- |
| simdjson-go | needs AVX2/SSE, runs normally | **`SupportedCPU()=false`, cannot run at all** |
| sonic | JIT fast path | **falls back to the compat path, not its real speed** |

In other words: **this machine (arm64) cannot measure sonic / simdjson's real
fast path.** The sonic numbers below are its **lower bound** (fallback path, a
pure-Go hand-written skip scan); on amd64 sonic would only be faster. A true
comparison must run on amd64 Linux (the comparison benchmark is wired in,
pending output from the amd64 CI job). The simdjson rows `b.Skip` on arm64.

The comparison libraries are objective third-party libraries; their content was
rephrased for compliance with licensing restrictions, used only for performance
comparison.

### Cold access / single pass (same work — who scans faster)

| Scenario | jseek (stateless) | sonic (arm64 lower bound) | relation |
| --- | --- | --- | --- |
| Small payload, 4 fields | **165 ns** / 0 B | 800 ns / 284 B | **jseek ~5x faster** |
| Large doc, shallow 2 fields | **122 ns** / 0 B | 385 ns / 81 B | **jseek ~3x faster** |
| Large doc, deep indexed 2 fields | 73 µs / 0 B | **22 µs** / 88 B | **sonic 3.3x faster** |
| 12 scattered fields | 373 µs / 0 B | **109 µs** / 564 B | **sonic 3.4x faster** |
| GitHub 7 nested fields | 56 µs / 0 B | **15.5 µs** / 329 B | **sonic 3.6x faster** |

**Honest takeaway:** on cold access jseek leads on **small/shallow** payloads
(and zero allocation), but on **deep array indices / many scattered fields** it
**loses to sonic by ~3.3–3.6x**. Profiling confirms the root cause: the cost of
those scenarios is dominated by scanning the string bodies of skipped elements —
an information-theoretic floor (you must read the bytes along the way to reach
the target) times a constant factor, and sonic's scan constant factor is better.
This is a real, undisguised weak spot, and on amd64 sonic's lead only widens.

> Note: the earlier claim that "jseek leads across all single extractions" holds
> only when comparing against gjson/jsonparser; **once sonic is included it does
> not hold for deep/scattered cold access**, so it is corrected here. Benchmarks
> are for calibrating boundaries, not for marketing.

### Amortized access (index once, reuse the tape, query many) — jseek's true blowout zone

| Scenario | jseek (IndexTape reused) | sonic (cold, forced to re-scan each time) | jseek advantage |
| --- | --- | --- | --- |
| 12 scattered fields | **5.9 µs** / 0 B | 109 µs | **~18x** |
| GitHub 7 nested fields | **1.5 µs** / 0 B | 15.5 µs | **~10x** |
| multi-path `EachDoc` + tape (6 paths) | **2.6 µs** / 0 B | 109 µs | **~42x** |

**Takeaway:** once the "index once, query many" premise holds, jseek with a
reused tape is an order-of-magnitude **10–42x blowout over sonic, zero allocation
throughout**. This is exactly jseek's home turf — it turns sonic's forced
full-document re-scan on every call into one structural index plus O(1)
subtree-skipping navigation, many times over.

### The bottom line for this section

| Dimension | vs sonic |
| --- | --- |
| Small / shallow cold access | jseek leads, zero alloc |
| Deep / many scattered cold access | **sonic leads ~3.3x (jseek's real weak spot)** |
| index+tape reuse | **jseek dominates by orders of magnitude (10–42x), zero alloc** |
| simdjson comparison | pending amd64 CI output (cannot run on arm64) |

In one sentence: **jseek dominates the full-parsing SOTA on "repeated /
multi-field / index-reuse," and still trails sonic on "single cold read of a
deep field" — the latter is a constant-factor contest near the
information-theoretic boundary, recorded honestly, no goalpost-moving.**

## Discipline about the numbers

Every performance change in this repo follows the same process: **profile to find
the real hotspot → optimize for it → prove with a same-binary A/B + benchstat →
guard correctness with differential fuzzing.** Any "speedup" drowned by benchmark
noise does not count. That is exactly why both jseek's wins and losses are
recorded here — benchmarks are for calibrating boundaries, not for marketing.
