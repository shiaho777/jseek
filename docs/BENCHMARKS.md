# jseek benchmarks & methodology

This document is a complete, reproducible record of jseek's performance —
**including the workloads where jseek loses**. The value of a benchmark is in
honestly marking the boundaries, not in showing only the wins.

**Last refreshed:** 2026-07-16 (Apple M4 Pro, Go 1.23.4, median of 3×300ms runs).

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
| Sampling | 3 runs × 300ms per case, medians reported |
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
| Small payload, 4 fields | **128 ns** / 0 B | 286 ns / 0 B | 359 ns / 144 B | ~719 ns / 284 B |
| Large doc, shallow fields | **93 ns** / 0 B | 122 ns / 0 B | 156 ns / 16 B | ~350 ns / 81 B |
| Large doc, deep indexed field (2 Gets) | **3.05 µs** / 0 B | 247 µs / 0 B | 88 µs / 16 B | 22.7 µs / 90 B |
| Same, via `GetFields` (1 seek + sibling harvest) | **1.58 µs** / 0 B | — | — | — |
| Large doc, full ArrayEach | **85 µs** / 0 B | 213 µs / 0 B | 298 µs / 188 KB | — |

**Takeaway:** jseek leads the lazy class on every row, at **zero allocation**.
FASS equal-size array strides flipped the old "deep index is expensive" story:
`users[250]` is now **~25× faster than gjson** and **~6.5× faster than sonic's
arm64 path**, still 0 B. Sibling `GetFields` roughly halves that again.

---

## Result 1b: whole-array field harvest

500 user objects, read `username` + `followers` from each.

| Approach | time | allocs |
| --- | --- | --- |
| jseek `EachArrayFields` | **76.5 µs** | 0 B |
| jseek `ArrayEach` + 2×`Get` per element | 82.6 µs | 0 B |
| jsonparser `ArrayEach` + 2×`Get` | 208 µs | 0 B |
| gjson `ForEach` + 2× field access | 276 µs | 188 KB |

**Takeaway:** one member pass per object for all keys beats the classic
"iterate elements, then N× extract" pattern; both jseek paths crush the
lazy competitors, zero allocation.

## Result 2: multi-path (single pass)

Six scattered paths on the large fixture (`meta`, `page`, three `users[i]`,
`trailer`).

| Engine | time | allocs |
| --- | --- | --- |
| jseek `EachKey` (compiled, FASS array jumps) | **39.3 µs** | 0 B |
| jseek N× `Get` | 47.5 µs | 0 B |
| gjson `GetManyBytes` | 196 µs | 536 B |

**Takeaway:** `EachKey` now reuses the same FASS `findIndex` path as `Get` for
array segments (sorted index edges, relative stride between them). It matches
N×`Get` and is **~4× faster than gjson**, zero allocation.

---

## Result 3: index once, query many (flagship scenario)

Reading 12 scattered fields from one 24 KB document.

| Approach | time | allocs |
| --- | --- | --- |
| Stateless `Get` ×12 (re-scan each) | **86 µs** | 0 B |
| `IndexPooled` + 12 queries | ~206 µs | ~1 B |
| **Reused index, 12 queries** | **130 µs** | 0 B |
| gjson `GetManyBytes` | 469 µs | 1.16 KB |
| sonic (arm64 cold) | 119 µs | 572 B |

**Takeaway:** on this fixture FASS makes **stateless** multi-get the fastest
cold path (beats sonic and gjson, 0 B). A reused structural index is still the
right tool when queries fan out deeper or when Stage-1 is amortized across a
process lifetime; combined with tape (Result 4) it becomes an order-of-magnitude
win.

---

## Result 4: skip tape (O(1) subtree skipping)

On the same large document, linear skip vs tape skip (A/B, same binary).

| Scenario | linear | tape | speedup |
| --- | --- | --- | --- |
| 12 scattered fields (reused) | 130 µs | **5.7 µs** | ~23x |
| deep `users[499].name` | 24.3 µs | **1.58 µs** | ~15x |
| multi-path `EachDoc` | 50.9 µs (no tape) | **2.59 µs** (tape) | ~20x |
| multi-path `Each` (stateless baseline) | 48.1 µs | — | — |

**Takeaway:** the tape turns O(subtree) skipping into O(1) — still an
order-of-magnitude gain on deep/scattered access, queries still zero-allocation.

---

## Result 5: real-world — GitHub-style nested response

200 issues, reading 7 scattered fields.

| Approach | time | allocs |
| --- | --- | --- |
| jseek IndexTape (reused) | **1.42 µs** | 0 B |
| jseek stateless (FASS on nested issues) | **5.61 µs** | 0 B |
| sonic (arm64 cold) | 14.3 µs | 331 B |
| gjson GetMany | 50.2 µs | 664 B |
| jsonparser | 137 µs | 0 B |

**Takeaway:** FASS now applies to homogeneous object arrays **even when elements
contain nested object-arrays** (safety is the two-element `skipContainer`
confirm + landing re-validate, not a structural ban). Cold stateless GitHub is
**~2.5× faster than sonic arm64** and **~9× faster than gjson**, still 0 B.
IndexTape remains the multi-query blowout (~1.4 µs).

---

## Result 6: real-world — NDJSON log stream

5000 flat access-log records (~250 B each), read `latency_ms`, `status`, and
`client.region` per line.

| Approach | time | allocs |
| --- | --- | --- |
| jseek `StreamNDJSONEach` (line split + multi-path, early exit) | **1.16 ms** | 0 B |
| jseek `StreamNDJSON` + 3×`Get` | 1.30 ms | 0 B |
| jseek `StreamBytes` + `EachKey` | 1.32 ms | 0 B |
| jseek `StreamBytes` + 3×`Get` | 1.58 ms | 0 B |
| gjson (pre-split lines + 3×`Get`) | 1.70 ms | 0 B |
| jseek `Decoder` (io.Reader) | 2.18 ms | ~66 KB |

**Takeaway:** the old NDJSON weak spot is closed. `StreamNDJSON` avoids a
per-record `skipContainer` by SWAR newline scanning; `Paths.Each` stops once
every requested path has been hit (skips trailing `trace_id`-sized junk). Net:
**~32% faster than gjson** on the same shape, still zero allocation.

## Result 7:## Result 7: scan micro-throughput (string body)

4 KB quote-free string body (M4 Pro).

| Scanner | throughput |
| --- | --- |
| naive 1 byte/step | ~3.5 GB/s |
| SWAR-8 (`purego` floor) | ~13.2 GB/s |
| NEON dispatch (arm64 native) | **~40.7 GB/s** |

**Takeaway:** the portable SWAR floor is ~3–4× naive; shipped NEON is another
~3× on top for long string bodies. Navigation cost on real documents is often
dominated by container skip + structural decisions, which is why FASS (skipping
whole equal-size objects without re-parsing) moves end-to-end numbers more than
raw GB/s alone.

---

## Result 8: vs the full-parsing SOTA — sonic (honest calibration)

| Library | amd64 | arm64 (this machine) |
| --- | --- | --- |
| simdjson-go | AVX2/SSE path | **`SupportedCPU()=false`** |
| sonic | JIT fast path | **compat path (not JIT)** |

arm64 sonic numbers are a **lower bound** on sonic's strength (compat is slower
than JIT). Even so:

### Cold access

| Scenario | jseek (stateless) | sonic (arm64) | relation |
| --- | --- | --- | --- |
| Small, 4 fields | **128 ns** / 0 B | ~719 ns / 284 B | **jseek ~5.6×** |
| Large, shallow 2 fields | **93 ns** / 0 B | ~350 ns / 81 B | **jseek ~3.8×** |
| Large, deep indexed 2 fields | **3.05 µs** / 0 B | 22.7 µs / 90 B | **jseek ~6.5×** (FASS) |
| 12 scattered fields | **86 µs** / 0 B | 119 µs / 572 B | **jseek ~1.4×** |
| GitHub 7 nested fields | **5.61 µs** / 0 B | 14.3 µs / 331 B | **jseek ~2.5×** |

**Updated takeaway (2026-07-16):** FASS + confirmed equal-size strides closed the
old deep-array and GitHub cold losses vs sonic arm64 on homogeneous issue/
user arrays — including elements that embed nested object-arrays. The former NDJSON weak spot is now a lead via `StreamNDJSON` + early-exit
multi-path (Result 6).

### Amortized access (IndexTape reused)

| Scenario | jseek IndexTape | sonic cold | jseek advantage |
| --- | --- | --- | --- |
| 12 scattered fields | **5.7 µs** / 0 B | 119 µs | **~21×** |
| GitHub 7 nested fields | **1.42 µs** / 0 B | 14.3 µs | **~10×** |
| multi-path `EachDoc` + tape | **2.59 µs** / 0 B | 119 µs | **~46×** |

### Bottom line

| Dimension | vs sonic (arm64) |
| --- | --- |
| Small / shallow cold | jseek leads, 0 alloc |
| Homogeneous deep array cold | **jseek leads (FASS), 0 alloc** |
| Nested homogeneous cold (GitHub issues) | **jseek leads ~2.5×** |
| IndexTape reuse | **jseek 10–46×, 0 alloc** |

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

## Discipline about the numbers

Every performance change in this repo follows the same process: **profile to find
the real hotspot → optimize for it → prove with a same-binary A/B → guard
correctness with differential fuzzing.** Any "speedup" drowned by benchmark
noise does not count. That is exactly why both jseek's wins and losses are
recorded here — benchmarks are for calibrating boundaries, not for marketing.
