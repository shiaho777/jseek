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
│   SWAR + AVX2 (amd64) / NEON (arm64); purego → SWAR only     │
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
| `index.go` / `index_ste.go` | Stage-1 structural index + **STE** template expansion for object arrays |
| `index_nav.go` | Stage-2 navigation over the index + topology-constant array stride + `Document` getters |
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
| `stream.go` | streaming `Decoder` / `NewNDJSONDecoder` (arrays + line-mode NDJSON) |
| `stream_bytes.go` | in-memory `StreamBytes` / `StreamNDJSON` / `StreamNDJSONEach` |
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

## The SWAR seam (and shipped SIMD)

`scan_swar.go` processes eight bytes per step inside a 64-bit register using
branch-free bit tricks (`zeroByteMask`). It is the portable floor for string
bodies and whitespace. Dispatch lives in `scan_dispatch_*.go`:

| Build | Backend |
| --- | --- |
| `purego` or non-x86/arm64 | SWAR only |
| `amd64 && !purego` | AVX2 string scan (`indexQuoteOrBackslashAVX2`) + SWAR whitespace |
| `arm64 && !purego` | NEON string scan + `skipContainerNEON` for object/array skip |

Nothing above the byte-scanning / container-skip layer changes across backends.
Assembly frames must stay in lockstep with Go prototypes (`go vet` is the CI
gate that catches `$0-N` / `ret` vs `ret1` mistakes).

### Array strides (FASS)

`findIndexObjectStride` in `navigate.go` accelerates minified homogeneous object
arrays — including elements that embed nested objects/arrays (GitHub-style
`issues[].labels[]`). Pure endpoint arithmetic only runs after **two**
consecutive elements agree on `skipContainer` length (`strideConfirmed`);
bulk/direct landings call `skipContainer` again. Nested structure is allowed;
safety is confirm+landing validation, not a ban on nested object-arrays.

### Sibling multi-key scan

`fields.go` (`GetFields` / `EachField` / `EachFieldInto` / `EachArrayFields`)
matches several keys under one object (or every object in an array) in a single
member walk, using a small active-key set rather than a full multi-path trie.

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
   but need not match any particular library. Fast-path array strides over
   homogeneous object elements (FASS equal-size jumps in `navigate.go`) only
   activate after two consecutive `skipContainer` lengths match, and direct
   landings are re-validated — nested object-arrays inside elements are allowed;
   endpoint shape alone is never enough to accept an index.
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
- **CI** (`.github/workflows/ci.yml`) hard-gates on build, vet, tests, `-race`,
  and the `jseeksafe` / `purego` builds on amd64 + arm64 (`ci success` aggregates
  the matrix). Fuzz **seed corpus** is required after tests; short generative
  fuzz is informational. A nightly workflow runs long generative campaigns.
  Concurrent same-ref runs cancel in progress so only the tip commit finishes.

## Structural Template Expansion (STE)

Stage-1 normally touches every string body. For **homogeneous object arrays**
(the dominant real-world shape behind `users[]`, `issues[]`, log batches), STE
does something different:

1. Fully index the first object of an equal-size run → a template of
   `(kind, relative_offset)` pairs.
2. Confirm the next object matches length and template anchors.
3. **Expand** the template across the rest of the run (bulk-expand with
   mid/quartile probes on long minified runs) — no interior string scan.
4. On digit-growth / size change, reseed a new template and continue.

Navigation, topology stride, and the skip tape see a normal dense structural
index; they do not know STE ran. Correctness falls back to full indexing with
output rollback if a run cannot be confirmed.

## Topology-constant array stride (indexed)

Stateless navigation already has FASS equal-size object strides. The **indexed**
path uses a different lever: when consecutive array elements share the same
sequence of structural kinds (same topology), `findIndexIndexed` jumps by the
measured structural step instead of visiting every element. Validation uses
mid/quartile topology probes and first-key checks. Because the step is in the
structural array — not in raw bytes — digit growth and other equal-topology,
unequal-size layouts still accelerate. Combined with the skip tape this makes
deep `users[N]` index queries competitive with (and often faster than) cold
stateless FASS.

