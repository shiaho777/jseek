# jseek

**The fastest way to pull values out of JSON in Go — zero allocations, no structs, no full parse.**

`jseek` is a high-performance, zero-allocation JSON *value extractor* for Go. You
give it a path; it walks the raw bytes lazily, skips every subtree it does not
need, and hands back a slice pointing straight into your original buffer — no
decoding the whole document, no struct definitions, no allocations on the read
path.

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
across all of them (see [`BENCHMARKS.md`](BENCHMARKS.md)). It gets there by
combining three things no other lazy extractor brings together:

1. **Skip-subtree navigation** — you never pay to parse data you didn't ask for.
2. **A SWAR scan core** (eight bytes per word) behind a seam where hand-written
   SIMD can drop in later, with no API change.
3. **An index-once / query-many engine** plus pinned and columnar APIs that turn
   repeated work into one-time work — where the lead over everything else widens
   to **double-digit multiples**.

The honest boundary: `jseek` does not decode whole documents into structs, and on
a couple of narrow workloads a SIMD full-parser or `gjson` still edges it out —
we publish exactly where, and how to reproduce it, in
[`BENCHMARKS.md`](BENCHMARKS.md). For "reach in and grab specific values from
large or unpredictable JSON," nothing in Go is faster.

## Design

`jseek` is built in layers so that the performance-critical core can evolve
(pure-Go today, SIMD tomorrow) without changing the public API:

| Layer | Responsibility |
| ----- | -------------- |
| Public API | `Get`, typed getters, `ArrayEach`, `ObjectEach` |
| Navigation | skip-subtree traversal to locate keys & indices |
| Structural scan | locate JSON structural bytes (pure-Go now; SIMD next) |
| Buffer & memory | zero-copy slices, no allocation on the read path |

The string/value skip loops are isolated precisely so that an AVX2/AVX-512
(amd64) and NEON (arm64) Stage-1 scanner can drop in behind them later. The
public API does not change when that lands.

### Scanning core: SWAR today, hand-written SIMD next

The hottest loop in JSON traversal is scanning string contents for the next
quote or backslash, and strings dominate real payload bytes. `jseek` does this
with **SWAR** (SIMD Within A Register): it loads eight bytes into a 64-bit word
and locates the next structural byte with branch-free bit tricks, processing 8
bytes per step instead of one. This is genuine data-parallel scanning that works
on **every** architecture Go targets, with no assembly and no portability risk.

Hand-written AVX2/AVX-512 and NEON kernels are a planned increment that slots in
behind the same function boundary; SWAR is the portable floor, not the ceiling.

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
| 12 scattered fields | 130 µs | 5.0 µs | **~26x** |
| deep `users[499].name` | 24.6 µs | 1.3 µs | **~18x** |

The tape costs one extra `uint32` per structural (it roughly doubles the
transient index), released with the `Document`. It is opt-in so plain `Index`
stays lean; reach for it when navigation, not scanning, is your bottleneck.

`Document` is read-only and safe for concurrent queries. Measured on a 24 KB
document, reading 12 scattered fields (Apple M4 Pro):

| Approach | time | allocs |
| --- | --- | --- |
| stateless `Get` ×12 (re-scan each) | 398 µs | 0 |
| `IndexPooled` + 12 queries | 165 µs | 2 (pooled) |
| reused index, 12 queries | **89 µs** | **0** |
| gjson `GetManyBytes` | 509 µs | 13 |

The more fields you read per document, the larger the win. The Stage-1 scanner
is the same SWAR core described below, so the SIMD/NEON milestone accelerates
this path too.

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
| `Each` (stateless, re-scan) | 157 µs | 1x |
| `EachDoc` (indexed, no tape) | 53 µs | ~3x |
| `EachDoc` (indexed + tape) | **2.6 µs** | **~61x** |

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
dec := jseek.NewDecoder(resp.Body)
err := dec.ForEach(func(elem []byte) error {
    name, _ := jseek.GetString(elem, "user", "name")
    // ... process one record; elem is valid only for this call
    return nil
})
```

`Decoder` auto-detects `[...]` arrays versus NDJSON / whitespace-separated
values. Set `Decoder.MaxValue` to cap per-element size and reject hostile input
with `ErrTooLarge`.

When the whole input is already in memory, `StreamBytes` walks it directly with
**zero allocation and zero copy** (each element aliases the input), which is
faster than the buffered reader:

```go
jseek.StreamBytes(data, func(elem []byte) error {
    id, _ := jseek.GetInt(elem, "id")
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
(ubuntu) and arm64 (macOS)**: build, `go vet`, tests, the `-race` detector, the
`jseeksafe` build, and a fuzz smoke run on each target. A nightly workflow runs
longer fuzz campaigns. This dual-architecture gate is the prerequisite for
landing hand-written SIMD kernels (AVX2/AVX-512 on amd64, NEON on arm64) safely.

Two behaviors are intentional and documented, matching `jsonparser`/`gjson`
rather than `encoding/json`:

- **Invalid UTF-8**: `jseek` returns the original bytes faithfully; it does not
  perform lossy `U+FFFD` replacement.
- **Duplicate keys**: `jseek` returns the **first** occurrence (`encoding/json`
  keeps the last). RFC 8259 leaves this implementation-defined.

## Status

The full feature set is in place and rigorously tested: the lazy `Get` family,
the "index once, query many" engine (`Index`/`IndexTape`), `Pin` and columnar
`Transpose` for repeated access, multi-path `EachKey`/`GetMany`, generic
accessors, `Set`/`Delete` mutation, dotted-path and JSON Pointer syntaxes,
contextual errors, and memory-bounded streaming (`Decoder`/`StreamBytes`).

Correctness is enforced by differential fuzz tests against `encoding/json` and
across jseek's own engines (tens of millions of executions each), and CI runs the
suite, the race detector, and the `jseeksafe`/`purego` builds on both amd64 and
arm64. The public API is considered stable.

The byte scanner uses SWAR today; hand-written AVX2/AVX-512 and NEON kernels are
an optional future increment behind the existing scan seam — measured to be
worth single-digit percent here, so they are not a prerequisite (see
[`BENCHMARKS.md`](BENCHMARKS.md)).

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for the design and
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
| Small payload, 4 fields | **145 ns** / 0 B | 276 ns / 0 B | 349 ns / 144 B |
| Large doc, shallow fields | **106 ns** / 0 B | 116 ns / 0 B | 158 ns / 16 B |
| Large doc, deep indexed field | **70 µs** / 0 B | 238 µs / 0 B | 88 µs / 16 B |
| Large doc, full ArrayEach | **131 µs** / 0 B | 211 µs / 0 B | 289 µs / 184 KB |
| Multi-path (6 fields, 1 pass) | **148 µs** / 0 B | — | 194 µs / 536 B |
| Index reused, 12 fields | **120 µs** / 0 B | — | 460 µs / 1.2 KB |
| Deep access + tape | **1.6 µs** / 0 B | — | — |

`jseek` leads on single-field, multi-path, and (especially) indexed/reused access,
and is zero-allocation across all of them. **It is not universally fastest:** on
tiny in-memory NDJSON records read field-by-field, `gjson` is ~7.5% faster — see
[`BENCHMARKS.md`](BENCHMARKS.md) for the full results including where `jseek`
loses, the methodology, and how to reproduce. Numbers vary by machine and
payload; verify on your own hardware and data.
