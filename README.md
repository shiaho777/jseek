[![CI](https://github.com/shiaho777/jseek/actions/workflows/ci.yml/badge.svg)](https://github.com/shiaho777/jseek/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/shiaho777/jseek.svg)](https://pkg.go.dev/github.com/shiaho777/jseek)
[![Go Report Card](https://goreportcard.com/badge/github.com/shiaho777/jseek)](https://goreportcard.com/report/github.com/shiaho777/jseek)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**`jseek` is a high-performance, zero-allocation JSON *value extractor* for Go. You
give it a path; it walks the raw bytes lazily, skips every subtree it does not
need, and hands back a slice pointing straight into your original buffer — no
decoding the whole document, no struct definitions, no allocations on the read
path.**

That is the most common real-world JSON job — **reaching into large, dynamic
payloads and grabbing the few fields you actually want** (3rd-party APIs, event
streams, logs, gateways) — and `jseek` is built to do it faster than anything
else in its class.

```go
import "github.com/shiaho777/jseek"

data := []byte(`{"user":{"name":"Ada","followers":42},"tags":["a","b"]}`)

name, _ := jseek.GetString(data, "user", "name")        // "Ada"
n, _    := jseek.GetInt(data, "user", "followers")      // 42
tag, _  := jseek.GetString(data, "tags", "[1]")         // "b"
```

## State of the art for lazy JSON extraction

JSON libraries fall into two camps:

- **Full parsers** (`encoding/json`, `goccy/go-json`, `bytedance/sonic`,
  `simdjson-go`) decode the *entire* document. Use these when you genuinely need
  every field as Go values.
- **Lazy extractors** (`buger/jsonparser`, `tidwall/gjson`) read only the fields
  you ask for — historically one byte at a time.

`jseek` is the **state of the art in the lazy-extraction class**: head-to-head on
identical fixtures it beats `jsonparser` and `gjson` (the current leaders) on
single-field, multi-path, and repeated-access workloads, at **zero allocations**
across all of them (see [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md)). It gets there by
combining three things no other lazy extractor brings together:

1. **Skip-subtree navigation** — you never pay to parse data you didn't ask for,
   with FASS equal-size strides on homogeneous object arrays.
2. **A SWAR + SIMD scan core** — portable 8-byte SWAR everywhere; AVX2 (amd64)
   and NEON (arm64) string/container kernels behind the same seam (`purego`
   forces SWAR only).
3. **Structural Template Expansion + index-once / query-many** plus `GetFields`, pinned, and columnar
   APIs that turn repeated work into one-time work — where the lead over
   everything else widens to **double-digit multiples**.

The honest boundary: `jseek` does not decode whole documents into structs, and on
a couple of narrow workloads a SIMD full-parser or `gjson` still edges it out —
we publish exactly where, and how to reproduce it, in
[`docs/BENCHMARKS.md`](docs/BENCHMARKS.md). For "reach in and grab specific values from
large or unpredictable JSON," nothing in Go is faster.

## Design

`jseek` is built in layers so the performance-critical core can evolve without
changing the public API:

| Layer | Responsibility |
| ----- | -------------- |
| Public API | `Get`, typed getters, `GetFields`/`EachField`, `EachKey`, `ArrayEach`/`ObjectEach`, `Index`/`Pin`/`Transpose`, mutation, streaming |
| Navigation | skip-subtree traversal, FASS equal-size array strides, multi-key object scan |
| Structural scan | SWAR portable floor + arch SIMD (AVX2 / NEON) behind build tags |
| Buffer & memory | zero-copy slices, no allocation on the read path |

### Scanning core: SWAR floor + shipped SIMD

The hottest loop in JSON traversal is scanning string contents for the next
quote or backslash, and strings dominate real payload bytes. Every architecture
gets a **SWAR** (SIMD Within A Register) portable floor: eight bytes per 64-bit
word with branch-free bit tricks.

On native builds (`!purego`) the same seam dispatches to hand-written kernels:

- **amd64** — AVX2 string-body scan (`scan_simd_amd64.s` / `scan_string_amd64.go`)
- **arm64** — NEON string-body scan and container skip
  (`scan_simd_arm64.s`, `skipContainerNEON`)

Build with `-tags purego` to force the portable SWAR path for debugging or
targets without assembly. The public API never changes across these backends.

### Array navigation: FASS equal-size strides

Homogeneous object arrays (common in API lists and event batches) enable a
**Structural Template Expansion** on Stage-1 (synthesize structurals for equal-size object-array runs) plus **fingerprint-anchored structural stride**: after two consecutive elements
verify the same `skipContainer` length, jseek jumps by fixed size instead of
re-parsing each sibling. Direct multi-element jumps re-validate the landing
object so endpoint shape alone cannot accept a false index on malformed input.

## The "index once, query many" engine

When you need **many** fields from **one** document, the stateless `Get`
re-scanning from the top each call is wasteful. `Index` performs a single
structural-scan pass (Stage 1) and returns a reusable `Document`; each query
then navigates the compact structural index (Stage 2) instead of re-reading raw
bytes. Skipping a nested subtree becomes a depth scan over index entries, not a
byte re-scan. This is one of jseek's biggest levers — on repeated access the
lead over other extractors grows to double-digit multiples.

```go
doc := jseek.Index(data)          // one Stage-1 pass
name, _ := doc.GetString("user", "name")
n, _    := doc.GetInt("user", "followers")
ok      := doc.Exists("user", "avatar", "url")
// ... dozens more queries, all sharing the single index
```

For per-request hot paths, `IndexPooled` draws the index buffer from a pool;
call `Document.Free()` when done to recycle it:

```go
doc := jseek.IndexPooled(reqBody)
defer doc.Free()
```

To process a stream of documents with zero per-document allocation, keep one
`Document` and `Reset` it:

```go
var doc jseek.Document
for _, msg := range messages {
    doc.Reset(msg)
    id, _ := doc.GetInt("id")
    // ...
}
```

### Skip-pointer tape (deep navigation)

By default, skipping over a nested container during navigation walks the
structural entries of that subtree. For documents where you reach **deep** array
elements or step past **large** sibling subtrees, build the optional
*skip-pointer tape* with `IndexTape` (or `doc.WithTape()`): it precomputes each
container's matching closer, so skipping a whole subtree becomes O(1).

```go
doc := jseek.IndexTape(data)
name, _ := doc.GetString("users", "[499]", "name") // jumps, doesn't walk
```

Measured on the 24 KB document (Apple M4 Pro, reused index, 0 allocs/query):

| Query | linear skip | with tape | speedup |
| --- | --- | --- | --- |
| 12 scattered fields | 130 µs | 5.7 µs | **~23x** |
| deep `users[499].name` | 24.3 µs | 1.58 µs | **~15x** |

The tape costs one extra `uint32` per structural (it roughly doubles the
transient index), released with the `Document`. It is opt-in so plain `Index`
stays lean; reach for it when navigation, not scanning, is your bottleneck.

`Document` is read-only and safe for concurrent queries. Measured on a 24 KB
document, reading 12 scattered fields (Apple M4 Pro):

| Approach | time | allocs |
| --- | --- | --- |
| stateless `Get` ×12 (FASS, re-scan each) | **86 µs** | **0** |
| reused index, 12 queries | 130 µs | 0 |
| IndexTape, 12 queries | **5.7 µs** | **0** |
| gjson `GetManyBytes` | 469 µs | 13 |

The more fields you read per document, the larger the win. Stage-1 scanning
uses the same SWAR/SIMD core as the rest of the package, so string-heavy
documents benefit on both the index build and the stateless path.

## API

### `Get`

```go
func Get(data []byte, keys ...string) (value []byte, dataType ValueType, offset int, err error)
```

Returns the raw value bytes (aliasing `data`), its `ValueType` (`String`,
`Number`, `Object`, `Array`, `Boolean`, `Null`, or `NotExist`), the offset just
past the value, and an error. Strings are returned **without** surrounding
quotes and **without** unescaping. Objects and arrays include their delimiters.
With no keys, `Get` returns the first value in `data` (handy for stream
fragments and array elements).

Array elements are addressed with bracketed indices: `jseek.Get(data, "users",
"[0]", "name")`.

### Typed getters

```go
func GetString(data []byte, keys ...string) (string, error)   // decodes escapes (allocates)
func GetStringUnsafe(data []byte, keys ...string) (string, error) // zero-copy view, no unescape
func GetBytes(data []byte, keys ...string) ([]byte, error)    // raw value, zero-copy
func GetInt(data []byte, keys ...string) (int64, error)       // rejects floats
func GetFloat(data []byte, keys ...string) (float64, error)
func GetBoolean(data []byte, keys ...string) (bool, error)
func Exists(data []byte, keys ...string) bool
```

### Generic accessors (Go 1.21+)

A single, type-safe entry point instead of remembering one method per type:

```go
name := jseek.Or[string](data, "anonymous", "user", "name")
n, err := jseek.At[int64](data, "user", "followers")
```

`At[T]` returns an error on a missing path or type mismatch; `Or[T]` returns a
fallback instead. `T` may be `string`, `bool`, `int64`, or `float64`.

### Iteration

```go
func ArrayEach(data []byte, cb func(value []byte, dt ValueType, off int) bool, keys ...string) error
func ObjectEach(data []byte, cb func(key, value []byte, dt ValueType, off int) bool, keys ...string) error
```

Both are allocation-free; return `false` from the callback to stop early.

### Multi-path extraction (single pass)

When you need several fields from one document, `EachKey` walks the bytes **once**
and reports every requested path, sharing common prefixes and skipping
everything else. Compile the path set once and reuse it for an allocation-free
hot loop:

```go
q := jseek.CompileStrings(
    []string{"meta", "version"},
    []string{"users", "[0]", "name"},
    []string{"users", "[42]", "followers"},
)
q.Each(data, func(idx int, value []byte, vt jseek.ValueType, err error) {
    // idx identifies which path matched
})
```

One-shot helpers `EachKey` (byte paths) and `EachKeyStrings` (string paths)
compile on each call for convenience.

For maximum speed on large documents, run a compiled path set over an indexed
(optionally taped) `Document` with `EachDoc`: navigation reuses the structural
index and O(1) subtree skipping instead of re-scanning bytes.

```go
q := jseek.CompileStrings(paths...)
doc := jseek.IndexTape(data)
q.EachDoc(doc, func(idx int, value []byte, vt jseek.ValueType, err error) { ... })
```

Measured on the 24 KB document (6 scattered paths, reused index, 0 allocs):

| Engine | time | vs stateless |
| --- | --- | --- |
| `Each` (stateless + FASS) | 48 µs | 1x |
| `EachDoc` (indexed, no tape) | 51 µs | ~1x |
| `EachDoc` (indexed + tape) | **2.6 µs** | **~18x** |

For ordered, typed results, `GetMany` returns a `Result` per path in a single
pass:

```go
res := jseek.GetMany(data,
    []string{"name"}, []string{"age"}, []string{"admin"},
)
name := res[0].String()
age, _ := res[1].Int()
admin, _ := res[2].Bool()
```

### Sibling fields under one object (`GetFields` / `EachField`)

When several keys live under the **same** object (or under one path prefix), a
full multi-path trie is more than you need. `GetFields` / `EachField` /
`EachFieldInto` walk that object once, matching every requested key in a single
pass — the hot path for "this record, these N columns":

```go
// path is the parent object; keys are siblings under it.
res, err := jseek.GetFields(data, []string{"users", "[250]"}, "username", "followers", "email")
// res[i] is a Result for keys[i]; missing keys are NotExist.

// Zero-allocation callback form; EachFieldInto reuses a caller offset buffer.
jseek.EachField(data, []string{"users", "[250]"},
    []string{"username", "followers"},
    func(idx int, value []byte, vt jseek.ValueType, err error) {
        // idx is the position in the keys slice
    })
```

On large minified object arrays this pairs with FASS strides: jump to the
element, then harvest multiple fields without re-seeking.

### Array column harvest (`EachArrayFields`)

When you need the **same sibling keys from every object** in an array (analytics,
ETL, fan-out over `users[]`), `EachArrayFields` walks the array once and, for
each object element, harvests all requested keys in a **single member pass** —
no per-element re-seek and no N×`Get` over the element:

```go
err := jseek.EachArrayFields(data, []string{"users"},
    []string{"username", "followers"},
    func(elem, key int, value []byte, vt jseek.ValueType, err error) bool {
        // elem = array index, key = index into the keys slice
        return true // false stops early
    })
```

On the large fixture (500 users × 2 fields) this is ~76 µs / 0 B vs ~83 µs for
`ArrayEach` + 2×`Get` per element, and ~3–3.5× faster than gjson/jsonparser on
the same shape.

### Path syntaxes

Besides variadic segments, paths can be written as a single string in two
notations:

```go
jseek.GetPath(data, "users[1].name")     // dotted path, bracket indices
jseek.GetPointer(data, "/users/1/name")  // RFC 6901 JSON Pointer
```

Both are also available on an indexed `Document` (`doc.GetPath`,
`doc.GetPointer`).

### Errors

The fast getters return bare sentinel errors (`ErrKeyPathNotFound`,
`ErrUnexpectedType`, `ErrOverflow`, `ErrMalformedJSON`) for zero-overhead
handling. The generic `At[T]` accessor wraps failures in a `*PathError` that
records which path segment failed and the expected vs actual type, while still
matching the sentinels via `errors.Is`:

```go
_, err := jseek.At[int64](data, "user", "age")
if errors.Is(err, jseek.ErrKeyPathNotFound) { /* ... */ }
var pe *jseek.PathError
if errors.As(err, &pe) { fmt.Println(pe.At, pe.Got, pe.Want) }
```

### Mutation

```go
func Set(data []byte, setValue []byte, keys ...string) ([]byte, error)
func Delete(data []byte, keys ...string) []byte
func AppendSet(dst, data, setValue []byte, keys ...string) ([]byte, error)
func AppendDelete(dst, data []byte, keys ...string) ([]byte, bool)
```

`Set` returns a new document with the value at the path replaced, creating any
missing object keys (and nested objects) along the way. `Delete` returns a new
document with the value at the path removed, fixing up commas so the result
stays valid. Both leave the caller's input untouched.

Internally each mutation resolves to a **single contiguous edit** — replace one
byte range, or remove one byte range — located in one downward pass over the
path. So `Set`/`Delete` allocate exactly once regardless of how deep the path
is (no per-level intermediate slices). For hot loops, `AppendSet` and
`AppendDelete` write the result into a caller-supplied buffer; reuse one scratch
buffer and the mutation is **amortized zero-allocation**:

```go
out, _ := jseek.Set([]byte(`{"user":{"name":"old"}}`), []byte(`"new"`), "user", "name")
// {"user":{"name":"new"}}

out = jseek.Delete([]byte(`{"a":1,"b":2}`), "a")
// {"b":2}

// zero-allocation hot loop: reuse one buffer across many mutations
buf := make([]byte, 0, 256)
for _, rec := range records {
    buf, _ = jseek.AppendSet(buf[:0], rec, []byte(`true`), "processed")
    // ... use buf; valid until the next AppendSet
}
```

### Streaming (memory-bounded huge inputs)

For inputs too large to hold in memory — a top-level array of records or
newline-delimited JSON (NDJSON) — `Decoder` reads from an `io.Reader` and yields
one complete element at a time. Memory stays bounded by the largest single
element, not the whole stream, and each element is a self-contained value you
can run the full jseek API on (including `Index`).

```go
dec := jseek.NewDecoder(resp.Body)       // JSON arrays / mixed values
// dec := jseek.NewNDJSONDecoder(resp.Body) // JSON Lines / NDJSON (faster)
err := dec.ForEach(func(elem []byte) error {
    name, _ := jseek.GetString(elem, "user", "name")
    // ... process one record; elem is valid only for this call
    return nil
})
```

`NewDecoder` auto-detects `[...]` arrays versus whitespace-separated values.
For known NDJSON / JSON Lines over `io.Reader`, use `NewNDJSONDecoder` (line mode,
no value-framing tax). Set `Decoder.MaxValue` to cap per-element size and reject hostile input
with `ErrTooLarge`.

When the whole input is already in memory, `StreamBytes` walks it with
**zero allocation and zero copy** (each element aliases the input):

```go
jseek.StreamBytes(data, func(elem []byte) error {
    id, _ := jseek.GetInt(elem, "id")
    return nil
})
```

For **JSON Lines / NDJSON** already in memory, prefer
`StreamNDJSON` — it splits on newlines with a SWAR scanner instead of
`skipContainer` per record. Pair with a compiled multi-path matcher and early
exit once all fields are found:

```go
q := jseek.CompileStrings(
    []string{"latency_ms"}, []string{"status"}, []string{"client", "region"},
)
_ = jseek.StreamNDJSONEach(data, q, func(idx int, value []byte, vt jseek.ValueType, err error) error {
    // one member pass per line; stops after the last needed key
    return nil
})
```

### Repeated queries on stable data: `Pin`

When the **same** document is queried repeatedly (a hot config, a reference
table), `Pin` learns each path's structural trajectory once, so every subsequent
read is a near-direct address with no key search. It is ~3.6x faster than cold
`Get` on repeated lookups, and the learned trajectory is a *cache, not a
contract*: every read verifies the full key chain and transparently falls back
to a full search if the document's shape drifts, so it can never return a wrong
value.

```go
doc := jseek.Index(config)
q := doc.Pin([]string{"limits", "rps"}, []string{"service", "region"})
for { // many times
    rps, _, _ := q.Get(0)
    _ = rps
}
```

### Repeated analytics on a batch: columnar `Transpose`

This is jseek's biggest lever. When you aggregate or scan the **same field across
a batch of similarly-shaped records many times** (dashboards, multi-metric jobs,
repeated filtering), row-wise access re-navigates every record on every pass.
`Transpose` does the JSON work **once**, extracting a field from every record
into a contiguous native slice; subsequent passes are plain slice scans with no
JSON parsing at all.

```go
// records is [][]byte, e.g. NDJSON lines
lat := jseek.TransposeInt(records, 0, "latency_ms")  // one pass → []int64
// now aggregate as many times as you like, at native-slice speed
var sum, max int64
for _, v := range lat { sum += v; if v > max { max = v } }
```

Measured (5000 records, 50 aggregation passes): **~30x faster** than row-wise
`Get` (and than gjson), growing without bound as passes increase — at 200 passes
it is ~93x. `Transpose` (multi-column) and `TransposeInt/Float/String/Bool/Raw`
are available; all verify each record and fall back per-record on shape drift,
so columns always reflect true values.

This does not break any law of physics: it converts *N repeated navigations*
into *one navigation + N native scans*. The win comes from eliminating repeated
work, not from parsing faster than reading the bytes.

## Allocation & safety

Every read operation is **zero-allocation** except `GetString`, which must copy
to safely produce an unescaped, immutable Go string. `GetStringUnsafe` and
`GetBytes` return views that alias your input buffer — fast, but only valid
while that buffer is unmodified and alive.

The zero-copy string view uses `unsafe` by default. Build with `-tags jseeksafe`
to get a fully-safe (copying) implementation with no `unsafe` at all.

`jseek` never mutates its input, so the package-level read functions are safe for
concurrent use on the same slice. A `Document`'s read methods are
concurrency-safe; its mutating methods (`Reset`, `WithTape`, `Free`, `Pin`) are
not. `Decoder` and `Pinned` are single-goroutine.

## Correctness

`jseek`'s contract is defined over RFC 8259-compliant JSON (which must be valid
UTF-8). Correctness is enforced by **differential fuzz tests against
`encoding/json`**:

- `FuzzGetAgainstStdlib` — extraction agrees with the standard library on key
  presence and scalar decoding.
- `FuzzDocumentMatchesGet` — the indexed engine returns exactly what stateless
  `Get` returns, for every path.
- `FuzzTapeMatchesGet` — the O(1) skip-tape navigation matches stateless `Get`.
- `FuzzEachKeyMatchesGet` — single-pass multi-path matching agrees with repeated
  `Get` calls.
- `FuzzEachDocMatchesEachKey` — the indexed/taped multi-path matcher agrees with
  the stateless one.
- `FuzzSetAgainstStdlib` / `FuzzDeleteAgainstStdlib` — mutations always produce
  valid JSON equal to the stdlib-computed expectation.
- `FuzzStreamMatchesArrayEach` — the streaming decoder yields the same elements
  as the in-memory `ArrayEach` scanner.
- `FuzzPinMatchesGet` — the learned-trajectory cache always equals stateless
  `Get`, even after rebinding to a differently-shaped document.
- `FuzzTransposeMatchesGet` — every cell of every transposed column equals a
  stateless `Get` on that record, across arbitrary mixed-shape batches.

Each has been run for tens of millions of executions with no divergences on
in-contract input.

Every change is gated by CI (`.github/workflows/ci.yml`) on **both amd64
(ubuntu) and arm64 (macOS)**. The hard gate is the `test` matrix: build, `go
vet`, unit tests, `-race`, and the `jseeksafe` / `purego` builds. A `ci success`
job aggregates that matrix so branch protection can require a single check.

After tests pass, CI also runs a **deterministic fuzz seed-corpus** pass on both
architectures and a short generative fuzz smoke (informational —
`continue-on-error`, so newly discovered edge cases do not red-X the whole
workflow). A nightly workflow (`.github/workflows/fuzz-nightly.yml`) runs the
full generative campaign for ~10 minutes per target. Concurrent runs on the same
ref cancel in progress so only the tip commit finishes. This dual-architecture gate is what keeps the shipped AVX2/NEON kernels and the
`purego` fallback honest across amd64 and arm64.

Two behaviors are intentional and documented, matching `jsonparser`/`gjson`
rather than `encoding/json`:

- **Invalid UTF-8**: `jseek` returns the original bytes faithfully; it does not
  perform lossy `U+FFFD` replacement.
- **Duplicate keys**: `jseek` returns the **first** occurrence (`encoding/json`
  keeps the last). RFC 8259 leaves this implementation-defined.

## Status

The full feature set is in place and rigorously tested: the lazy `Get` family,
sibling-field `GetFields`/`EachField`, the "index once, query many" engine
(`Index`/`IndexTape`), `Pin` and columnar `Transpose` for repeated access,
multi-path `EachKey`/`GetMany`, generic accessors, `Set`/`Delete` mutation,
dotted-path and JSON Pointer syntaxes, contextual errors, and memory-bounded
streaming (`Decoder`/`StreamBytes`/`StreamNDJSON`). Navigation includes FASS equal-size array
strides; scanning includes portable SWAR plus shipped AVX2/NEON kernels (opt out
with `-tags purego`).

Correctness is enforced by differential fuzz tests against `encoding/json` and
across jseek's own engines (tens of millions of executions each). CI's required
path runs the unit suite, the race detector, and the `jseeksafe`/`purego` builds
on both amd64 and arm64, plus fuzz seed-corpus checks; generative fuzz depth
lives in the nightly job. The public API is considered stable.

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design and
[`CONTRIBUTING.md`](CONTRIBUTING.md) to get involved.

## Benchmarks

Run them yourself — methodology over marketing:

```sh
cd bench
go test -bench=. -benchmem -count=6
```

The harness (`bench/`) compares `jseek` head-to-head against
**`buger/jsonparser`** and **`tidwall/gjson`** (the current SOTA lazy extractor)
on identical fixtures, reporting ns/op **and** allocs/op.

Representative results on an Apple M4 Pro (lower is better):

| Scenario | jseek | jsonparser | gjson |
| --- | --- | --- | --- |
| Small payload, 4 fields | **128 ns** / 0 B | 286 ns / 0 B | 359 ns / 144 B |
| Large doc, shallow fields | **93 ns** / 0 B | 122 ns / 0 B | 156 ns / 16 B |
| Large doc, deep indexed (2 Gets) | **3.0 µs** / 0 B | 247 µs / 0 B | 88 µs / 16 B |
| Same via `GetFields` | **1.79 µs** / 0 B | — | — |
| Large doc, full ArrayEach + 2 fields/elem | **83 µs** / 0 B | 208 µs / 0 B | 276 µs / 188 KB |
| Large doc, `EachArrayFields` (2 fields/elem) | **76 µs** / 0 B | — | — |
| Multi-path (6 fields, `EachKey`) | **46.7 µs** / 0 B | — | 196 µs / 536 B |
| Stateless 12 fields (FASS) | **86 µs** / 0 B | — | 469 µs / 1.2 KB |
| IndexTape, 12 fields | **5.7 µs** / 0 B | — | — |
| Deep access + tape | **1.58 µs** / 0 B | — | — |
| GitHub 7 fields (cold FASS) | **5.6 µs** / 0 B | 137 µs / 0 B | 50 µs / 664 B |

`jseek` leads the lazy class on single-field, multi-path, deep homogeneous arrays
(FASS), and IndexTape reuse — all **zero allocation**. Deep `users[250]` (~3 µs) and GitHub-style issue arrays (~5.6 µs cold) are both
ahead of sonic's arm64 path. NDJSON log harvest with `StreamNDJSONEach` leads
gjson on the former weak spot (~1.16 ms vs ~1.70 ms / 5000 lines). See
[`docs/BENCHMARKS.md`](docs/BENCHMARKS.md) for full boundaries and methodology.
