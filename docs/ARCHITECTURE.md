# jseek architecture

This document explains how jseek is built and why, so contributors can extend it
without breaking its core guarantees: **zero allocation on the read path**,
**correctness over valid JSON**, and **portability with an acceleration path**.

## The central idea

Most JSON libraries fall into two camps:

- **Full parsers** decode the entire document (`encoding/json`, `sonic`,
  `simdjson-go`).
- **Lazy extractors** read only requested fields (`jsonparser`, `gjson`).

jseek is a lazy extractor that borrows the *structural-index* technique from the
full-parser world. The result is "index once, query many": pay a single scan to
locate structure, then answer many path queries cheaply by navigating that
structure instead of re-reading bytes.

## Layers

```
┌────────────────────────────────────────────────────────────┐
│ Public API                                                   │
│   Get / GetString / GetInt / ...        (stateless)          │
│   Document.Get / ...                     (indexed)           │
│   At[T] / Or[T]                          (generic)           │
│   EachKey / Paths / GetMany              (multi-path)        │
│   Set / Delete                           (mutation)          │
│   Decoder                                (streaming)         │
│   GetPath / GetPointer                   (path syntaxes)     │
├────────────────────────────────────────────────────────────┤
│ Query / Navigation                                           │
│   seek (stateless)      seekIndexed (over structural index)  │
│   findKey / findIndex   compiled Paths trie                  │
├────────────────────────────────────────────────────────────┤
│ Structural scan (Stage 1)                                    │
│   skipString / skipValue / skipObject / skipArray            │
│   indexStructurals (builds the offset index)                 │
├────────────────────────────────────────────────────────────┤
│ Byte scanning core                                           │
│   SWAR: indexQuoteOrBackslash, indexSkipWhitespace           │
│   (SIMD AVX/NEON kernels slot in here behind build tags)     │
└────────────────────────────────────────────────────────────┘
```

## File map

| File | Responsibility |
| --- | --- |
| `types.go` | `ValueType`, sentinel errors |
| `scan.go` | structural skip primitives (`skipValue`, `skipObject`, ...) |
| `scan_swar.go` | SWAR byte scanner (8 bytes/word) — the acceleration seam |
| `navigate.go` | stateless key/index location (`seek`, `findKey`, `findIndex`) |
| `escape.go` | allocation-free escape compare and unescape (full `\uXXXX`) |
| `number.go` | allocation-free int/float/bool parsing |
| `jseek.go` | stateless public API + shared `valueAt` extractor |
| `index.go` | Stage-1 structural index builder (`Index`, `Document`, pool) |
| `index_nav.go` | Stage-2 navigation over the index + `Document` getters |
| `index_tape.go` | optional O(1) skip-pointer tape for deep navigation |
| `eachkey.go` | compiled multi-path matcher (`Paths`, `EachKey`) |
| `eachkey_doc.go` | index/tape-aware multi-path matcher (`EachDoc`) |
| `pin.go` | learned-trajectory cache for repeated queries (`Pin`) |
| `column.go` / `column_multi.go` | columnar transposition (`Transpose*`, `Frame`) |
| `result.go` | `Result` typed view + `GetMany` |
| `generic.go` | generic `At[T]` / `Or[T]` |
| `path.go` | dotted-path and JSON Pointer parsing |
| `errors.go` | `PathError` with path/offset/type context |
| `mutate.go` | `Set` / `Delete` |
| `stream.go` | streaming `Decoder` (arrays + NDJSON) |
| `bytes_unsafe.go` / `bytes_safe.go` | zero-copy vs safe string view (build tag) |

## Two scan engines, one extractor

There are two ways to locate a value:

1. **Stateless** (`seek` in `navigate.go`): walks the raw bytes from the start,
   skipping subtrees with `skipValue`. Used by package-level `Get`. No state, no
   setup cost — ideal for reading one field. Object keys are matched with
   `scanKey`, which fuses the close-quote scan and the key comparison into one
   scalar pass with early mismatch bail (object keys are short, so the SWAR
   scanner's per-call setup does not amortize there; values still use SWAR).

2. **Indexed** (`seekIndexed` in `index_nav.go`): walks a pre-built array of
   packed structural entries (3-bit kind + 29-bit offset). Skipping a subtree is
   a brace-depth scan over that small array rather than over raw bytes; the
   packed kind means the skip loop never touches the document. Used by
   `Document` — ideal for many fields from one document. An optional
   skip-pointer tape (`index_tape.go`) reduces subtree-skip to O(1), giving
   18–26x on deep/scattered access (measured).

Both converge on `valueAt` (in `jseek.go`) for the final value extraction, so they
share identical type/quote/offset semantics. The `FuzzDocumentMatchesGet` test
enforces that they always agree on valid JSON.

## The SWAR seam (and where SIMD goes)

`scan_swar.go` processes eight bytes per step inside a 64-bit register using
branch-free bit tricks (`zeroByteMask`). It is the hot loop for string bodies
and whitespace, which dominate real payload bytes. The exported entry points
`indexQuoteOrBackslash` / `indexSkipWhitespace` are a dispatch seam
(`scan_dispatch_*.go`): the portable build routes them to SWAR; a SIMD build
would route them to a hand-written kernel.

A hardware-SIMD implementation (AVX2/AVX-512 on amd64, NEON on arm64) would add
`scan_simd_<arch>.s` plus a Go stub and select it in `scan_dispatch_native.go`,
typically behind a runtime CPU-feature check. Nothing above the byte-scanning
core changes. A `purego` build tag always forces the SWAR path.

### Is hardware SIMD worth it? (measured)

We measured scan throughput on a 4 KB string body (Apple M4 Pro) to decide,
using a portable 16-byte-wide SWAR prototype as a stand-in for a wider vector:

| Scanner | throughput | vs previous |
| --- | --- | --- |
| naive (1 byte/step) | 3.3 GB/s | — |
| SWAR-8 (current) | 10.6 GB/s | **3.2x** |
| SWAR-16 (wider-lane prototype) | 11.5 GB/s | 1.09x |

The scalar→SWAR-8 step is the big win. Doubling the lane to 16 bytes adds only
~9%, which means the loop is already close to throughput-bound at this width.
Hand-written AVX2 (32 B) / NEON (16 B) would fall on the same flattening curve —
an estimated 10–30% on large inputs — at a large cost in assembly complexity and
cross-architecture maintenance. The seam exists so SIMD *can* be added when a
workload justifies it; for now SWAR is the right engineering trade-off. This
honest result is why SIMD is positioned as optional, not as a headline.

## Invariants contributors must preserve

1. **The read path allocates nothing** except where documented (`GetString`
   decodes escapes; `Index` allocates the offset array). Guard new code with
   allocation-sensitive benchmarks (`-benchmem`).
2. **Never mutate the input.** Reads return slices aliasing the caller's bytes;
   mutations return fresh buffers.
3. **The two scan engines must agree.** Any change to navigation must keep
   `FuzzDocumentMatchesGet` green.
4. **Correctness is differential.** New features that interpret JSON get a fuzz
   test against `encoding/json` or against an existing jseek path.
5. **Contract = valid UTF-8 JSON.** Behavior on malformed input must not panic,
   but need not match any particular library.
6. **The indexed engine is correct at any document size.** The structural index
   packs a 29-bit offset, so documents above 512 MiB cannot be indexed; rather
   than truncate the index (which would silently mis-answer queries into the
   tail), `Index`/`Reset` flag such documents `oversize` and every `Document`
   query transparently routes to the unlimited stateless scanner. The indexed
   and stateless engines must therefore stay interchangeable for any path — the
   same property `FuzzDocumentMatchesGet` enforces.

## Testing strategy

- **Unit tests** per feature (`*_test.go`).
- **Runnable examples** (`example_test.go`) double as documentation.
- **Differential fuzzers** (`*_fuzz_test.go`) compare jseek against `encoding/json`
  or cross-check jseek's own engines.
- **CI** runs build, vet, tests, `-race`, the `jseeksafe` build, and fuzz smoke on
  amd64 + arm64; a nightly workflow runs longer fuzz campaigns.
