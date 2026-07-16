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
| jseek `NewNDJSONDecoder` + `Paths.Each` (new each time) | 1.18 ms | 263 KB (bufio once) |
| jseek `StreamBytes` + `EachKey` | 1.28 ms | 0 B |
| jseek `StreamNDJSON` + 3×`Get` | 1.30 ms | 0 B |
| jseek `NewNDJSONDecoder` + 3×`Get` | 1.37 ms | 263 KB |
| jseek `StreamBytes` + 3×`Get` | 1.54 ms | 0 B |
| gjson (pre-split lines + 3×`Get`) | 1.73 ms | 0 B |
| jseek `NewDecoder` (generic value framing) | 2.17 ms | 66 KB |

**Takeaway:** in-memory and `io.Reader` NDJSON are both closed as weak spots.
`StreamNDJSONEach` is the zero-alloc in-memory lead (**~1.6× gjson**).
`NewNDJSONDecoder` + `Reset` matches it on the Reader path within noise while
keeping the bufio window warm. Multi-path early-exit still skips trailing junk
keys.
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

## Discipline about the numbers

Every performance change in this repo follows the same process: **profile to find
the real hotspot → optimize for it → prove with a same-binary A/B → guard
correctness with differential fuzzing.** Any "speedup" drowned by benchmark
noise does not count. That is exactly why both jseek's wins and losses are
recorded here — benchmarks are for calibrating boundaries, not for marketing.
