# jseek benchmarks & methodology

This document is a complete, reproducible record of jseek's performance —
**including the workloads where jseek loses**. The value of a benchmark is in
honestly marking the boundaries, not in showing only the wins.

**Last refreshed:** 2026-07-16 (Apple M4 Pro, Go 1.23.4, median of 5×200ms package benches + 3×150ms scan micro).

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
| Sampling | 5 runs × 200ms per case (package), medians reported |
| jseek build | native arm64 (`!purego`: NEON string/container + FASS strides) |

Absolute numbers vary by machine; reproduce on your own hardware and data and
look at the relative relationships.

## Comparison libraries

- [`buger/jsonparser`](https://github.com/buger/jsonparser) — the classic lazy
  extractor.
- [`tidwall/gjson`](https://github.com/tidwall/gjson) — the previous de-facto SOTA
  in this class.
- [`bytedance/sonic`](https://github.com/bytedance/sonic) — full-parser with lazy
  `Get`; on arm64 this is the **compat** path (not JIT). Still a strong lower
  bound for "how hard is full-parse cold access."

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

| Scenario | jseek | jsonparser | gjson | sonic (arm64) |
| --- | --- | --- | --- | --- |
| Small payload, 4 fields | **132 ns** / 0 B | 296 ns / 0 B | 371 ns / 144 B | 703 ns / 284 B |
| Large doc, shallow fields | **93.5 ns** / 0 B | 124 ns / 0 B | 165 ns / 16 B | 340 ns / 81 B |
| Large doc, deep indexed field (2 Gets) | **3.14 µs** / 0 B | 256 µs / 0 B | 97.3 µs / 16 B | 21.1 µs / 96 B |
| Same, via `GetFields` (1 seek + sibling harvest) | **1.62 µs** / 0 B | — | — | — |
| Large doc, full ArrayEach | **93.5 µs** / 0 B | 240 µs / 0 B | 310 µs / 188 KB | — |

**Takeaway:** jseek leads the lazy class on every row, at **zero allocation**.
FASS equal-size array strides flipped the old "deep index is expensive" story:
`users[250]` is now **~31× faster than gjson** and **~6.7× faster than sonic's
arm64 path**, still 0 B. Sibling `GetFields` roughly halves that again.
---

## Result 1b: whole-array field harvest

500 user objects, read `username` + `followers` from each.

| Approach | time | allocs |
| --- | --- | --- |
| jseek `EachArrayFields` | **80.3 µs** | 0 B |
| jseek `ArrayEach` + 2×`Get` per element | 93.5 µs | 0 B |
| jsonparser `ArrayEach` + 2×`Get` | 240 µs | 0 B |
| gjson `ForEach` + 2× field access | 310 µs | 188 KB |

**Takeaway:** one member pass per object for all keys beats the classic
"iterate elements, then N× extract" pattern; both jseek paths crush the
lazy competitors, zero allocation.
---

## Result 2: multi-path (single pass)

Six scattered paths on the large fixture (`meta`, `page`, three `users[i]`,
`trailer`).

| Engine | time | allocs |
| --- | --- | --- |
| jseek `EachKey` (compiled, FASS array jumps) | **45.1 µs** | 0 B |
| jseek N× `Get` | 48.5 µs | 0 B |
| gjson `GetManyBytes` | 219 µs | 536 B |

**Takeaway:** `EachKey` reuses the same FASS `findIndex` path as `Get` for
array segments (sorted index edges, relative stride between them). It matches
N×`Get` and is **~4.9× faster than gjson**, zero allocation.
---

## Result 3: index once, query many (flagship scenario)

Reading 12 scattered fields from one 24 KB document.

| Approach | time | allocs |
| --- | --- | --- |
| Stateless `Get` ×12 (re-scan each) | 80.0 µs | 0 B |
| `IndexPooled` + 12 queries (STE Stage-1) | **70.1 µs** | 0 B |
| **Reused index, 12 queries** | **49.7 µs** | 0 B |
| **Reused IndexTape, 12 queries** | **1.34 µs** | 0 B |
| gjson `GetManyBytes` | 497 µs | 1.18 KB |
| sonic (arm64 cold) | 100 µs | 578 B |

**Takeaway:** STE flipped the old cold-index tax: **one-shot `IndexPooled` + 12
queries now beats both FASS re-scan and sonic**, still ~0 B. Reused index widens
the gap; IndexTape is the multi-query blowout (~1.3 µs).
---

## Result 4: skip tape + topology-constant array stride

On the same large document, linear skip vs tape skip (A/B, same binary). Deep
array access uses **topology-constant array stride** on the structural index:
when consecutive elements share the same structural-kind sequence, the
navigator jumps by structural step instead of walking every element. Unlike
equal-byte FASS, this works with digit growth (variable number widths).

| Scenario | linear (topology) | tape (+ topology) | notes |
| --- | --- | --- | --- |
| 12 scattered fields (reused) | 49.5 µs | **1.34 µs** | ~37× with tape |
| deep `users[499].name` | **0.27 µs** | **0.12 µs** | topology O(1) probe |
| multi-path `EachDoc` | 47.9 µs (no tape) | **2.28 µs** (tape) | ~21× tape |
| multi-path `Each` (stateless baseline) | 42.0 µs | — | FASS |

**Takeaway:** topology stride collapses deep indexed array access from O(N) to
O(1) probes without requiring equal element sizes. Tape still wins on
scattered multi-field work; together they erase the old "index is slow for deep
arrays" story. Queries remain zero-allocation.
---

## Result 5: real-world — GitHub-style nested response

200 issues, reading 7 scattered fields.

| Approach | time | allocs |
| --- | --- | --- |
| jseek IndexTape (reused) | **1.46 µs** | 0 B |
| jseek stateless (FASS on nested issues) | **5.99 µs** | 0 B |
| jseek IndexTape per request (STE + tape) | 35.1 µs | ~0 B |
| sonic (arm64 cold) | 14.8 µs | 328 B |
| gjson GetMany | 57.2 µs | 664 B |
| jsonparser | 150 µs | 0 B |

**Takeaway:** FASS applies to homogeneous object arrays **even when elements
contain nested object-arrays** (safety is the two-element `skipContainer`
confirm + landing re-validate). Cold stateless GitHub is **~2.5× faster than
sonic arm64** and **~9.5× faster than gjson**, still 0 B. IndexTape remains the
multi-query blowout (~1.5 µs). Per-request IndexTape is for multi-query fan-out
after STE; for a handful of cold fields prefer stateless.
---

## Result 6: real-world — NDJSON log stream

5000 flat access-log records (~250 B each), read `latency_ms`, `status`, and
`client.region` per line.

| Approach | time | allocs |
| --- | --- | --- |
| jseek `StreamNDJSONEach` (line split + multi-path, early exit) | **1.11 ms** | 0 B |
| jseek `NewNDJSONDecoder` + `Reset` + `Paths.Each` | **1.10 ms** | **51 B / 1 alloc** |
| jseek `NewNDJSONDecoder` + `Paths.Each` (new each time, pooled) | 1.14 ms | ~0 B steady-state |
| jseek `StreamBytes` + `EachKey` | 1.28 ms | 0 B |
| jseek `StreamNDJSON` + 3×`Get` | 1.30 ms | 0 B |
| jseek `NewDecoder` (auto-detects NDJSON → line mode) | **1.44 ms** | 66 KB |
| jseek `NewNDJSONDecoder` + 3×`Get` | 1.52 ms | ~0 B steady-state |
| jseek `StreamBytes` + 3×`Get` | 1.54 ms | 0 B |
| gjson (pre-split lines + 3×`Get`) | 1.74 ms | 0 B |

**Takeaway:** every NDJSON path now beats gjson. The former weak spot —
`NewDecoder`'s generic value framing at 2.17 ms (slower than gjson's 1.73 ms)
— is closed: it now auto-detects a newline-delimited stream after the first
value and switches to line mode, landing at **~1.44 ms (~1.2× faster than
gjson)**. `NewNDJSONDecoder` buffers are pooled (`sync.Pool`), so per-request
construction no longer allocates a fresh 256 KiB bufio each time; with `Close`
or `Reset` reuse the steady-state allocation is ~0 (guarded by
`TestDecoderReuseZeroAlloc`). `StreamNDJSONEach` remains the zero-alloc
in-memory lead (**~1.6× gjson**). Multi-path early-exit still skips trailing
junk keys.
---

## Result 6b: Stage-1 index build — Structural Template Expansion (STE)

| Fixture | time | prior (no STE, A/B) | speedup |
| --- | --- | --- | --- |
| large (~24 KB, 500 users) | **18.8 µs** | ~65 µs | **~3.5×** |
| GitHub-style (~60 KB, 200 issues) | **15.8 µs** | ~30 µs | **~1.9×** |

**Takeaway:** **Structural Template Expansion** is a Stage-1 breakthrough unique
to jseek. On homogeneous object arrays it fully indexes only the first element
of each equal-size run, captures the structural kind/relative-offset template,
then **synthesizes** the remaining elements' structurals without re-scanning
string bodies — reseeding when digit growth changes object width. Combined with
short-string inline skip and NEON structural find, cold `Index` is no longer the
tax that made one-shot indexed multi-get lose to pure FASS.
---

## Result 7: scan micro-throughput (string body)

4 KB quote-free string body (M4 Pro).

| Scanner | throughput |
| --- | --- |
| naive 1 byte/step | ~2.9–3.4 GB/s |
| SWAR-8 (`purego` floor) | **~13.2 GB/s** |
| NEON dispatch (arm64 native) | **~40.0 GB/s** |

**Takeaway:** the portable SWAR floor is ~4× naive; shipped NEON is another
~3× on top for long string bodies. Navigation cost on real documents is often
dominated by container skip + structural decisions, which is why FASS / STE
(skipping whole equal-size objects without re-parsing) move end-to-end numbers
more than raw GB/s alone.
---

## Result 8: vs the full-parsing SOTA — sonic (honest calibration)

| Library | amd64 | arm64 (this machine) |
| --- | --- | --- |
| simdjson-go | AVX2/SSE path | **`SupportedCPU()=false`** |
| sonic | JIT fast path | **compat path (not JIT)** |

arm64 sonic numbers are a **lower bound** on sonic's strength (compat is slower
than JIT). Even so:

### Cold access

| Scenario | jseek (stateless / cold) | sonic (arm64) | relation |
| --- | --- | --- | --- |
| Small, 4 fields | **132 ns** / 0 B | 703 ns / 284 B | **jseek ~5.3×** |
| Large, shallow 2 fields | **93.5 ns** / 0 B | 340 ns / 81 B | **jseek ~3.6×** |
| Large, deep indexed 2 fields | **3.14 µs** / 0 B | 21.1 µs / 96 B | **jseek ~6.7×** (FASS) |
| 12 scattered fields (STE cold Index) | **70.1 µs** / 0 B | 100 µs / 578 B | **jseek ~1.4×** |
| 12 scattered fields (stateless) | 80.0 µs / 0 B | 100 µs / 578 B | **jseek ~1.3×** |
| GitHub 7 nested fields | **5.99 µs** / 0 B | 14.8 µs / 328 B | **jseek ~2.5×** |

**Updated takeaway (2026-07-16):** FASS + STE + topology stride closed the old
deep-array, cold-index, and GitHub losses vs sonic arm64 on homogeneous
issue/user arrays. NDJSON is a lead via `StreamNDJSON` / `NewNDJSONDecoder`
(Result 6).

### Amortized access (IndexTape reused)

| Scenario | jseek IndexTape | sonic cold | jseek advantage |
| --- | --- | --- | --- |
| 12 scattered fields | **1.34 µs** / 0 B | 100 µs | **~75×** |
| GitHub 7 nested fields | **1.46 µs** / 0 B | 14.8 µs | **~10×** |
| multi-path `EachDoc` + tape | **2.28 µs** / 0 B | 100 µs | **~44×** |

### Bottom line

| Dimension | vs sonic (arm64) |
| --- | --- |
| Small / shallow cold | jseek leads, 0 alloc |
| Homogeneous deep array cold | **jseek leads (FASS), 0 alloc** |
| Nested homogeneous cold (GitHub issues) | **jseek leads ~2.5×** |
| Cold Index multi-get (STE) | **jseek leads ~1.4×, 0 alloc** |
| IndexTape reuse | **jseek ~10–75×, 0 alloc** |

---

## What moved these numbers (2026-07-16)

1. **FASS confirm rule** — two `skipContainer` equal lengths before pure
   endpoint strides; direct landings re-validated (correctness + stable speed).
2. **Stable `strideOK` across digit-width size classes** — stop re-running
   `hasNestedObjectArray` on every `id:9`→`id:10` length change (was ~23% of
   deep-index CPU).
3. **`EachKey` array edges use `findIndex` / relative `findIndexObjectStride`**
   — multi-path no longer walks every preceding array element with `skipValue`.
4. **Drop the nested-object-array ban on FASS** — `labels:[{...}]` inside equal-size
   issue objects no longer disables strides; confirm+landing checks keep it safe.
5. **`EachArrayFields`** — single member pass per array element for column harvest.
6. **`StreamNDJSON` + `Paths` early-exit** — SWAR newline split; stop object walk when all paths hit.
7. **Topology-constant array stride** — indexed deep `arr[N]` jumps by structural step when element topologies match (digit growth OK).
8. **`NewNDJSONDecoder`** — line-mode Reader path for JSON Lines without full value framing.
9. **Structural Template Expansion (STE)** — synthesize Stage-1 structurals for equal-size object-array runs; skip interior re-scan.

## 2026-11 round: wide-key compare, division-free ints, exact whitespace mask

Same-binary A/B on Apple M4 Pro (go1.23.4, `-benchtime=300ms`, medians of 2–3):

| Micro | before | after | note |
| --- | --- | --- | --- |
| `scanKey`, 33-byte key | ~31 ns | ~19.5 ns | SWAR 8-byte compare + exact terminator mask; scalar kept below 16-byte targets |
| `scanKey`, 4-byte key | ~4.7 ns | ~5.2 ns | one extra length branch; within noise on full workloads |
| `GetInt`, 19-digit value | ~36 ns | ~31 ns | digit-count overflow bound replaces per-digit division |

Full-workload fixtures (Small / LargeShallow / LargeIndexed / ArrayEach /
GitHub / ManyFields / EachKey) are unchanged within run-to-run noise
(±5–10% thermal variance on this machine); an early wide-key threshold of
8 bytes measurably regressed short-key workloads (~10%) and was raised to
16 bytes after A/B isolation.

New zero-allocation APIs measured on the same machine:

| API | fixture | time | allocs |
| --- | --- | --- | --- |
| `Valid` | 22 KB mixed doc | ~21.5 µs (~1.0 GB/s) | 0 |
| `Count` | 500-element object array | ~5.3 µs | 0 |
| `GetPath` / `GetPointer`, simple paths | small doc | same as `Get` | **0** (was 1+ per call for the segment slice) |

Correctness note: the SWAR whitespace mask previously used the classic
zero-byte trick, whose borrow chain can misread a byte adjacent to a space
(e.g. `'!'`) as whitespace. The exact detector (`zeroByteMaskExact`) closes
this class; it was found by the new `FuzzValidMatchesStdlib` differential
fuzzer and is guarded by exhaustive mask property tests. Quote/backslash and
structural masks keep the classic trick because they re-check the located
byte; A/B showed no measurable cost either way on the full fixtures.

## 2026-11 round 2: wildcard/filter queries, GetString single copy

New gjson-compatible query surface (`Query`/`QueryFirst`), 500 records,
Apple M4 Pro, `-benchtime=300ms`, medians of 2:

| Scenario | jseek | gjson | ratio |
| --- | --- | --- | --- |
| `users.#.name` (all 500) | **~21 µs / 33 KB / 15 allocs** | ~68 µs / 151 KB / 36 allocs | ~3.2× faster, ~4.6× less memory |
| `users.#(age>40)#.name` (filtered, all) | **~37 µs / 16 KB** | ~62 µs / 67 KB | ~1.7× faster, ~4× less memory |

Semantics match gjson where gjson defines them (`#` array wildcard,
`#(...)` first-match, `#(...)#` all-matches, terminal `#` count); `*`
iterating object members is a documented jseek extension. Differential
gates: `TestQueryMatchesGJSON` + `FuzzQueryMatchesGJSON` (bench module).

`GetString` now decodes into one right-sized buffer instead of
append-then-copy: ~80 ns → ~70 ns on a 68-byte UA string (same 2 allocs;
the second is the runtime's string conversion, unavoidable for an immutable
decoded string).

**Recorded negative result:** a SWAR wide-compare for `keyMatches`
(multi-path key equality) was prototyped three ways — exact terminator
masks, compare-first with escape check on divergence, and varied thresholds
(16/32 bytes). All measured **slower** than the scalar loop even on 93-byte
keys: target-key loads are unaligned and the per-call safety check outweighs
the word-at-a-time gain. Reverted; the scalar loop stays. (`scanKey`'s fused
scan+compare wide path is a different shape and remains a win.)

## 2026-11 round 3: the CLI, and head-to-head vs jq on real files

jseek now ships `cmd/jseek` — a single static binary (query / filter / stream /
validate / compact / indent). Compared against **jq 1.7.1** on generated real
files, wall-clock via `/usr/bin/time -p`, outputs verified **byte-identical**
(`diff` on 50 000-line wildcard harvest and on minified output):

| Task | fixture | jseek | jq | speedup |
| --- | --- | --- | --- | --- |
| `meta.version` (one field) | 7.5 MB, 50 000 users | 0.02 s | 0.38 s | ~19× |
| `users.#.name` (all names) | 7.5 MB | 0.03 s | 0.48 s | ~16× |
| `users.#(age>60)#.email` | 7.5 MB | 0.03 s | 0.47 s | ~15× |
| `-n latency_ms` (NDJSON) | 17.6 MB, 200 000 lines | 0.05 s | 0.92 s | ~18× |
| `-c` (minify) | 7.5 MB | 0.02 s | 1.51 s | ~75× |

Why: jq builds a full in-memory tree per invocation; jseek's lazy walker touches
only the bytes a query needs, and the CLI streams NDJSON with bounded memory.
Single-field extraction is ~0.01–0.02 s regardless of document size (the field
is near the front), while jq's cost scales with the whole document.

New library APIs backing the CLI: `Compact` / `Indent` (match stdlib
byte-for-byte, `FuzzFormatMatchesStdlib`), `QueryTyped` (typed results so a
string `"42"` is never confused with the number `42`).

## 2026-11 round 4: advanced query syntax, strict validation, parallel NDJSON

Driven by a frontier survey (simdjson v5.2 On-Demand, Go 1.27 json/v2, jaq 3.x,
Glaze SWAR-first, Pison parallel indexing). Shipped the high-ROI items:

**Query expressiveness** (jq-compatible, output verified byte-identical to jq):
recursive descent `..key`, array slices `[start:end]` with negative bounds,
compound filters `#(a && b || c)#` with correct `&&`-over-`||` precedence.
Guarded by `FuzzQueryAdvancedNoPanic` (4.9M execs) + jq cross-checks. Core
harvest unchanged: `users.#.name` ~19 µs vs gjson ~64 µs (~3.3×).

**`ValidStrict`**: json/v2-style safety defaults (reject invalid UTF-8, lone
surrogates, duplicate keys). Strict subset of `Valid`; zero-alloc on
scalars/arrays, allocates only for the duplicate-key map. CLI: `-v -strict`.

**CLI parallel NDJSON (`-p N`)**: read/process/write pipeline, order preserved.
Measured on a 124 MB file (20 000 lines × 200 events), query `events.#.k`:

| workers | time |
| --- | --- |
| 1 | 0.29 s |
| 2 | 0.21 s |
| 4 | **0.16 s (~1.8×)** |
| 8 | 0.18 s |

**Recorded boundary:** parallelism only pays for compute-heavy per-line work.
Trivial field reads (`latency_ms`) are I/O-bound: `-p 1` 0.29 s vs `-p 8` 0.35 s
(coordination overhead dominates). The flag is documented accordingly; default
stays 1. Large single-doc queries remain ~7–23× faster than jq (`..city`:
0.01 s vs 0.23 s on 50 000 records).

**Deferred (honest):** `jsontext` interop needs Go 1.27 (local toolchain is
1.23.4 — cannot verify, so not shipped blind); Pison-style parallel structural
indexing and arm64 SVE kernels need hardware/CI this machine lacks.

## 2026-11 round 5: persistent index — the zero-parse data substrate

The strategic breakthrough: jseek's Stage-1 index is a pointer-free []uint32,
so it can be persisted and re-opened with ZERO re-scanning. This moves jseek
from "fast per-process extractor" toward "embeddable, shareable JSON storage"
(the SQLite-of-JSON direction). New APIs: Document.Persist, Open, View,
ViewTrusted, VerifyIndex, IndexSize.

Measured (Apple M4 Pro, 27 KB doc, open + deep GetInt), same-binary A/B:

| Path | time | allocs |
| --- | --- | --- |
| RebuildIndexTape (re-scan every open) | ~19.5 µs | 109 KB / 7 |
| Open (streaming decode + hash + offset verify) | ~20 µs | 74 KB / 7 |
| View (zero-copy + full hash & offset verify) | ~7.9 µs | 128 B / 1 |
| **ViewTrusted after one VerifyIndex** | **~126 ns** | **128 B / 1** |

The canonical mmap pattern (map once → VerifyIndex once → ViewTrusted many)
is **~155× faster and ~850× less allocating** than rebuilding. Cross-process
demo: writer seals store.json + store.jix; reader maps and sustains 100 000
opens + 2-field queries at ~229 ns each.

**Honest boundaries:**
- Index overhead is ~217% of payload WITH tape (structural ~108% + tape ~108%);
  plain Index (no tape) is ~108%. The substrate trades disk for zero-parse opens.
- ViewTrusted skips the per-open hash by design — it requires a one-time
  VerifyIndex on an immutable mapping. View (default) still hashes per open.
- The win is on REPEATED opens of STABLE data. For one-shot reads of changing
  data, stateless Get / Index remains the right tool.
- -tags jseeksafe decodes the arrays (3 allocs) instead of zero-copy
  reinterpreting; the guarantee there is safety, not zero allocation.

## 2026-11 round 6: substrate hardening + self-contained stores + CLI

- **Fast payload hash**: replaced byte-wise FNV-1a (~0.3 GB/s) with a
  word-at-a-time multiply-xor-rotate mix measured at **~6.7 GB/s** (~20×).
  This made the fully-verified `View` (~7.9 µs) **~2.5× faster than rebuilding**
  (~19.5 µs) — integrity no longer costs more than a re-scan. Format flags bit1
  records the hash algorithm; the format is experimental (pre-1.0).
- **Offset bounds validation**: every structural/tape offset is checked against
  the payload length on View/Open/VerifyIndex, so a corrupt-but-hash-matching
  image can never drive navigation out of bounds. Runs with the hash check;
  the trusted O(1) path skips it (already verified). ViewTrusted stays ~126 ns.
- **Self-contained stores** (`store.go`): `Seal`/`OpenSealed`/`ViewSealed`/
  `ViewSealedTrusted`/`VerifySealed` pack payload+index into one "JSEEKST1"
  file (payload padded to 4-byte alignment for zero-copy index reinterpret).
- **mmap store handle**: `OpenStore`/`Store.View`/`Store.Close` maps a sealed
  file read-only once, verifies once, hands out concurrent-safe O(1) Documents.
  unix uses syscall.Mmap; other platforms fall back to a one-time read.
- **CLI**: `jseek -seal in.json out.jstore` and `jseek -open out.jstore 'path'`
  expose the substrate from the terminal.
- **FuzzPersistRoundTrip**: persist→View/Open round-trip agreement + corrupt
  rejection on arbitrary bytes (5.7M execs clean). Full matrix + windows/linux
  cross-compilation green.

**Honest note:** CLI single-shot `-open` vs reading the raw .json is dominated
by process startup + file read (0.17s vs 0.19s over 20 runs on 7.4 MB) — the
substrate's real win is repeated in-process opens (the library numbers above),
not one-shot CLI invocations.

## 2026-11 round 7: NDJSON random-access store + zero-alloc reuse

- **NDJSON random-access store** (`ndjson_store.go`): `SealNDJSON` builds a
  per-record uint64 offset table + a sealed index per record; `OpenNDJSONStore`
  maps and validates once; `Record(i)` / `RecordInto` jump in O(1). `Each`
  scans with a reused Document (zero alloc). `AppendNDJSON` copies prior
  records verbatim and indexes only new ones (incremental write path).
  CLI: `-seal-n` / `-open-n ... -at N` / `-append-n`.

  Measured (5000 records, jump to record 4999 + GetInt), same-binary A/B:

  | Path | time | allocs |
  | --- | --- | --- |
  | Store.RecordInto(4999) | **~45 ns** | **0** |
  | Sequential scan to 4999 (plain NDJSON reader) | ~69 µs | 128 B / 2 |

  **~1500×** for random access vs sequential — the qualitative change from
  "logs can only be scanned" to "any record instantly". CLI `-at` over 50
  process invocations: 0.15 s total.

- **ViewInto / RecordInto**: zero-allocation trusted views that re-point an
  existing Document. Hot loops allocate **0** (verified by AllocsPerRun;
  skipped under jseeksafe where arrays are decoded).

- **Bug found & fixed by FuzzNDJSONStoreRoundTrip**: parseNDJSON did not set
  the image field, so Record's last-record index bound sliced out of range
  when called directly. Fixed; 4.2M fuzz execs clean.

**Honest boundaries:** the NDJSON store trades ~per-record index overhead for
O(1) access; it suits read-heavy stable logs, not high-frequency single-record
mutation (append rebuilds the tail table). Random-access win grows with record
count; for tiny files a sequential scan is fine.

## 2026-11 round 8: incremental index patch — the single-document write path

`PatchSet` / `PatchDelete` update a Document's index without a full re-scan:
structurals before the edit keep their offsets, structurals after shift by the
length delta, and only the edited region is re-scanned. `Store.Set` /
`Store.Delete` expose field-level writes on a sealed store (returning a new
image). Correctness gated by `FuzzPatchMatchesRebuild` (5.5M execs, patched
index == full rebuild on every probe).

**Honest measurement — this is a modest win, not a breakthrough:**

| Scenario (2000–5000 records) | FullRebuild | IncrementalPatch |
| --- | --- | --- |
| front edit, mixed doc | ~62 µs / 344 KB | ~53 µs / 189 KB (~17% faster, ~45% less mem) |
| front edit, string-heavy | ~153 µs | ~153 µs (parity) |
| tail edit, string-heavy | ~181 µs | ~176 µs (~3%) |

**Why the gain is limited:** a Set/Delete shifts every byte after the edit, so
the payload MUST be copied in full (contiguous JSON has no gap to exploit). The
memcpy dominates; the index re-scan it avoids was already the cheaper part. The
patch's real value is (a) it makes field-level writes on the substrate possible
at all without re-parsing string bodies, (b) ~45% less index allocation on
front edits, and (c) it is the foundation for a future persistent write path.
It is NOT a dramatic speedup, and we record it as such. A truly cheap write
would need a non-contiguous / paged payload format — a larger redesign, deferred.

## 2026-11 round 9: WAL overlay — genuinely cheap writes (the breakthrough)

Round 8 proved an in-place edit must copy the whole contiguous payload. Round 9
sidesteps that entirely with a **log-structured overlay** (the SQLite-WAL / LSM
idea): `WALStore` keeps the immutable sealed base plus an append-only edit log.
A write appends ONE small record (path + op + value) — **no payload copy, no
index rebuild**. Reads resolve the base zero-parse, then apply the latest
matching overlay edit; subtree reads affected by the overlay materialize on
demand. `Compact` folds the log into a fresh base, amortizing the one expensive
copy over many cheap writes. Optional WAL file persists edits for crash-safe
replay. CLI: `-wal` + `-wset`/`-wdel`/`-wget`/`-wcompact`.

**Measured write cost (5000-record ~330 KB doc), same-binary A/B:**

| Write method | time | allocs |
| --- | --- | --- |
| Full Set rewrite (copy whole payload) | ~82 µs | 344 KB |
| **WAL append (with applicability check)** | **~180 ns** | **~300 B** |

**~450× faster, ~1100× less memory.** And the defining property — **write cost
is independent of document size:**

| doc size | WAL write |
| --- | --- |
| 100 records | ~163 ns |
| 5 000 records | ~163 ns |
| 50 000 records | ~186 ns |

The write touches only the edit, never the payload. This is genuinely cheap
writes — the thing round 8 said needed a redesign, achieved instead by changing
the *write model* (append-only overlay) rather than the payload format.

**Honest boundaries (as of round 9; the first one is broken in round 10):**
- ~~Reads of an overlay-dirtied subtree materialize (O(document)) every time.~~
  → Round 10 caches the materialization against the edit generation: repeated
  dirty-subtree reads are O(1); only the first read after an edit rebuilds.
- The overlay is path-keyed; an edit's applicability is validated with a cheap
  O(path-depth) walk at write time (this is the ~180 ns, vs ~80 ns pure append).
- Compaction is the one O(document) operation; amortize it over many writes.
- Concurrent-safe (RWMutex); base mapping immutable. FuzzWALMatchesMaterialize
  gates overlay reads == fully materialized document (clean after fixing root /
  ancestor-prefix dirty detection found by the fuzzer).

## 2026-11 round 10: breaking the dirty-read boundary — generation cache

Round 9's remaining boundary: every read of an overlay-dirtied subtree
materialized the whole document (O(document) per read). Round 10 breaks it with
a **generation-keyed materialization cache**: WALStore tracks a `gen` counter
bumped on every append/compact; `materialize` caches its result against `gen`
and reuses it while the edit set is unchanged. Repeated dirty-subtree reads go
from O(document) to O(1); the cost is paid once per edit-set change, not per read.

**Measured (5000-record doc, one edit dirties the items subtree, read a field
inside it), same-binary A/B:**

| Read method | time | allocs |
| --- | --- | --- |
| Fresh materialize every read (round 9 behavior) | ~1.15 ms | 934 KB |
| **Cached repeated read (round 10)** | **~93 ns** | **24 B** |

**~12,000× faster, ~39,000× less memory** for the read-after-write pattern.
Cache invalidation is exact: any append or compact bumps `gen`, forcing a
rebuild on the next dirty read (TestWALMaterializeCacheInvalidation). The cache
is guarded by its own mutex so reads build it without blocking writers; the
returned slice is shared read-only. Race detector + FuzzWALMatchesMaterialize
clean.

**Remaining honest boundary (as of round 10; broken in round 11):** the FIRST
dirty read after each edit still materialized O(document). → Round 11 replaces
full-document materialization with subtree-local resolution.

## 2026-11 round 11: subtree-local resolution — the last boundary broken

Round 10's remaining boundary: the first dirty read after an edit materialized
the WHOLE document. Round 11 breaks it with **subtree-local resolution**
(`resolveSubtree`): a dirty read touches only the requested subtree plus the
edits inside it — never the rest of the document.

Mechanism:
- Find the nearest ancestor overlay edit (its value replaces the subtree root)
  or locate the base subtree via the **structural index** (`seekIndexed`, not a
  stateless byte scan) and take a zero-copy slice of just that subtree.
- Collect only descendant edits under the read path, strip the prefix to make
  them relative, and apply them to the subtree slice with PatchSet/PatchDelete.
- Exact-path hits and unaffected subtrees still take the zero-parse fast path.

**Measured: FIRST dirty read (cache cold) of an element inside a large edited
array, same-binary A/B:**

| doc size | old full-materialize | new subtree-local |
| --- | --- | --- |
| 100 records | 7.3 µs / 18 KB | 1.1 µs / 1.4 KB |
| 5 000 records | 1.16 ms / 934 KB | 2.2 µs / 1.4 KB |
| 50 000 records | **103.7 ms / 9.46 MB** | **11 µs / 1.4 KB** |

At 50 000 records: **~9,400× faster, ~6,700× less memory.** The old path's
allocation grew linearly with the document (18 KB → 934 KB → 9.46 MB); the new
path is **flat at ~1.4 KB regardless of document size** — both time and memory
now scale with the *subtree*, not the document. The first-dirty-read
O(document) boundary is broken.

**Remaining honest note:** the residual time growth (1.1 → 11 µs across
100 → 50 000) is structural-index navigation to locate the element, not copying;
allocation is flat. A read of the ENTIRE dirty document (root) still assembles
the whole thing — unavoidable, since the result IS the whole document. The
breakthrough is that reading a *part* no longer pays for the *whole*.
FuzzWALMatchesMaterialize clean (fixed root-read descendant collection found by
the fuzzer); full matrix + race green.

## Discipline about the numbers

Every performance change in this repo follows the same process: **profile to find
the real hotspot → optimize for it → prove with a same-binary A/B → guard
correctness with differential fuzzing.** Any "speedup" drowned by benchmark
noise does not count. That is exactly why both jseek's wins and losses are
recorded here — benchmarks are for calibrating boundaries, not for marketing.
