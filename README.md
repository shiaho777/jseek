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

## The `jseek` CLI — jq's job, at lazy-extraction speed

jseek ships a **single static binary with zero runtime dependencies** that
queries, filters, streams, validates, and formats JSON from your terminal:

```sh
go install github.com/shiaho777/jseek/cmd/jseek@latest
# or grab a prebuilt binary (linux/darwin/windows, amd64/arm64) from Releases:
#   https://github.com/shiaho777/jseek/releases

jseek user.name config.json                 # extract a field
jseek 'users.#.name' api.json               # every element's name
jseek 'users.#(age>30)#.email' api.json     # filtered, all matches
jseek '..name' api.json                     # recursive descent (any depth)
jseek 'nums[1:3]' api.json                  # array slice ([-2:] from end)
jseek -n 'latency_ms' logs.ndjson           # stream 200k log lines, bounded memory
jseek -n -p 8 'events.#.k' heavy.ndjson     # parallel workers (order preserved)
jseek -c big.json                           # minify
jseek -i big.json                           # pretty-print
jseek -v untrusted.json                     # validate (exit 0/1)
jseek -v -strict untrusted.json             # + reject bad UTF-8/surrogates/dup keys
cat api.json | jseek 'users[0].name'        # stdin
```

`-p N` parallelizes per-line NDJSON work with order preserved. It pays off for
compute-heavy queries (e.g. `events.#.k` over 200-event records: ~1.8× at 4
workers on a 124 MB file); trivial field reads are I/O-bound and stay fastest
at `-p 1`.

**Measured against `jq 1.7.1` on real files** (Apple M4 Pro, output verified
byte-identical to jq):

| Task | file | jseek | jq | speedup |
| --- | --- | --- | --- | --- |
| Extract one field | 7.5 MB JSON | **0.02 s** | 0.38 s | **~19×** |
| Harvest 50 000 names (`users.#.name`) | 7.5 MB JSON | **0.03 s** | 0.48 s | **~16×** |
| Filter + project (`age>60` → email) | 7.5 MB JSON | **0.03 s** | 0.47 s | **~15×** |
| Stream 200 000 NDJSON lines | 17.6 MB | **0.05 s** | 0.92 s | **~18×** |
| Minify (`-c`) | 7.5 MB JSON | **0.02 s** | 1.51 s | **~75×** |

jq parses the whole document into a tree on every invocation; jseek walks only
the bytes each query needs. The gap widens with document size. See
[`docs/BENCHMARKS.md`](docs/BENCHMARKS.md) for methodology and the library-level
comparisons against gjson/jsonparser/sonic.

## Beyond extraction: a zero-parse JSON data substrate

jseek's structural index is a compact, pointer-free array — which means it can
be **persisted once and re-opened by any process with zero re-parsing**. This
turns jseek from a per-request extractor into an embeddable, shareable JSON
storage layer: the "SQLite of JSON" for applications that store JSON and read
fields from it repeatedly (config stores, document caches, IPC shared state,
edge-device state, feature stores).

```go
// One self-contained file: payload + index, sealed once.
jseek.Seal(file, data)                  // -> store.jstore

// Any process: mmap it, verify once, then open in O(1) forever.
s, _ := jseek.OpenStore("store.jstore") // maps read-only + verifies
defer s.Close()
doc, _ := s.View()                      // O(1), zero re-parse, zero-copy
rps, _ := doc.GetInt("config", "limits", "rps")
```

Or from the terminal:

```sh
jseek -seal big.json big.jstore         # build once
jseek -open big.jstore 'users.#.name'   # query forever, zero re-parse
```

**Measured** (Apple M4 Pro, 27 KB doc, open + deep query):

| Path | time | allocs |
| --- | --- | --- |
| Rebuild index from bytes (every process today) | ~19.5 µs | 109 KB |
| `View` (zero-copy + full hash & offset verify) | ~7.9 µs | 128 B |
| `ViewTrusted` after one-time `VerifyIndex` | **~126 ns** | **128 B** |

**~155× faster open, ~850× less memory** on the trusted path; even the
fully-verified `View` is ~2.5× faster than rebuilding. The payload hash is a
word-parallel mix running at **~6.7 GB/s** (vs ~0.3 GB/s for byte-wise FNV),
and every index offset is bounds-checked, so a corrupt or stale image is
rejected (`ErrBadIndex`) — never an out-of-bounds read. A cross-process demo
sustains **100 000 opens + 2-field queries at ~229 ns each**.

APIs:
- **Self-contained stores**: `Seal`, `OpenSealed`, `ViewSealed` /
  `ViewSealedTrusted`, `VerifySealed`, and `OpenStore` / `Store.View` /
  `Store.Close` (mmap-backed, concurrent-safe, read-only).
- **Split index + payload**: `Document.Persist`, `Open`, `View` /
  `ViewTrusted`, `VerifyIndex`, `Document.IndexSize`.
- **Zero-allocation reuse**: `ViewInto(d, idx, data)` re-points an existing
  Document — a hot loop that opens repeatedly allocates **nothing**.
- **Field-level writes**: `PatchSet` / `PatchDelete` update a Document's index
  incrementally (no full re-scan); `Store.Set` / `Store.Delete` return a new
  sealed image with the edit applied. Stores stay immutable — write the new
  image and re-open. (The payload copy dominates cost, so this enables writes
  more than it accelerates them; see BENCHMARKS.md.)

### NDJSON random-access store

Logs, events, and metrics are the largest large-JSON shape, and a plain
`.ndjson` file can only be scanned sequentially. `SealNDJSON` builds a
per-record offset table plus a sealed index per record, so **record N is
reachable in O(1)** and queryable with zero re-parsing:

```go
jseek.SealNDJSON(out, ndjsonReader)     // build once
s, _ := jseek.OpenNDJSONStore("logs.njstore")
defer s.Close()
d, _ := s.Record(999999)                // O(1) jump, zero re-parse
lat, _ := d.GetInt("latency_ms")
s.Each(func(i int, d *jseek.Document) bool { /* zero-alloc scan */ ; return true })
```

```sh
jseek -seal-n logs.ndjson logs.njstore
jseek -open-n logs.njstore 'latency_ms' -at 999999   # one record, instant
jseek -append-n logs.njstore new.ndjson              # incremental append
```

**Measured** (5000 records, jump to record 4999 + field read): **~45 ns / 0
allocs** via the store vs **~69 µs** for a sequential scan that must walk every
prior record — **~1500×**. `AppendNDJSON` copies prior records verbatim and
indexes only the new ones (O(new records)). Guarded by
`FuzzNDJSONStoreRoundTrip`.

The zero-copy view reinterprets the index arrays in place (default build);
`-tags jseeksafe` decodes them safely instead. mmap is used on unix; other
platforms fall back to a one-time read. The format is experimental (pre-1.0).

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
we publish every comparison (including where a full-parser JIT path may still
compete on other archs) in
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
| 12 scattered fields | 49.5 µs | 1.34 µs | **~37×** |
| deep `users[499].name` | 0.27 µs | 0.12 µs | **~2.2×** (already O(1) via topology) |

The tape costs one extra `uint32` per structural (it roughly doubles the
transient index), released with the `Document`. It is opt-in so plain `Index`
stays lean; reach for it when navigation, not scanning, is your bottleneck.

`Document` is read-only and safe for concurrent queries. Measured on a 24 KB
document, reading 12 scattered fields (Apple M4 Pro):

| Approach | time | allocs |
| --- | --- | --- |
| stateless `Get` ×12 (FASS, re-scan each) | 80.0 µs | **0** |
| cold `IndexPooled` + 12 (STE) | **70.1 µs** | **0** |
| reused index, 12 queries | 49.7 µs | 0 |
| IndexTape, 12 queries | **1.34 µs** | **0** |
| gjson `GetManyBytes` | 497 µs | 13 |

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
| `Each` (stateless + FASS) | 42.0 µs | 1× |
| `EachDoc` (indexed, no tape) | 47.9 µs | ~1.1× |
| `EachDoc` (indexed + tape) | **2.28 µs** | **~18×** |

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

On the large fixture (500 users × 2 fields) this is ~80 µs / 0 B vs ~94 µs for
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

### Validation (`Valid`)

Gateway primitive: check untrusted input once, then extract with the lazy API.
`Valid` reports whether the input is exactly one well-formed JSON value
(literal spelling, number grammar, escape legality, container structure). It
agrees with `encoding/json.Valid` on all inputs — like the stdlib it is a
syntax check, so lone surrogates and invalid UTF-8 inside strings are accepted
(decoders replace them later). Zero allocation.

```go
if !jseek.Valid(body) {
    // reject malformed input before doing any work
}
name, _ := jseek.GetString(body, "user", "name")
```

Measured ~1.0 GB/s on a 22 KB document (Apple M4 Pro), 0 allocs.

`ValidStrict` adds the safety defaults popularized by `encoding/json/v2`:
it additionally rejects invalid UTF-8, lone/unpaired surrogate escapes, and
duplicate object member names. Use it at trust boundaries where ambiguous
input must be refused rather than silently reinterpreted:

```go
if !jseek.ValidStrict(untrusted) {
    // reject: bad UTF-8, lone surrogates, or duplicate keys
}
```

`ValidStrict` is a strict subset of `Valid` (anything it accepts also passes
`Valid` and decodes cleanly with the stdlib). It allocates only for the
duplicate-key check; scalar/array validation stays zero-allocation. The CLI
exposes it as `jseek -v -strict`.

### Counting (`Count`, `#` paths)

`Count` returns the number of array elements or object members at a path
without materializing anything — zero allocation:

```go
n, _ := jseek.Count(data, "users")          // len(users)
m, _ := jseek.Count(data)                   // top-level member count
```

Dotted paths support a trailing `.#` count marker, and `GetPath` returns the
count as a `Number` value:

```go
v, _, _, _ := jseek.GetPath(data, "users.#")   // e.g. []byte("500")
n, _ := jseek.GetInt(data, "users", jseek.CountSeg) // native segment form
```

A literal `"#"` object key stays addressable via the native segment form
(`Get(data, "#")`); the count marker is an internal sentinel that cannot
collide with real keys.

### Wildcard & filter queries (`Query` / `QueryFirst`)

gjson-compatible shape queries for harvesting many values at once:

```go
names, _ := jseek.Query(data, "users.#.name")          // every element's name
first, _, _ := jseek.QueryFirst(data, "users.#(age>30).name") // first match
admins, _ := jseek.Query(data, "users.#(active==true)#.email") // ALL matches
n, _     := jseek.Query(data, "users.#")               // array length
```

- `#` mid-path iterates array elements; terminal `users.#` is the count.
- `#(key op value)` filters elements (`== != > >= < <=`; strings, numbers,
  bools, null; bare key = existence). Default continues with the **first**
  match (gjson semantics); append `#` — `#(...)#` — for **all** matches.
  Combine with `&&` / `||` (`&&` binds tighter): `#(age>30 && active==true)#`.
- `..key` is recursive descent (jq-style): every member named `key` at any
  depth — `..name`, `users..address.city`.
- `[start:end]` slices arrays (jq/Python-style); bounds optional, negatives
  count from the end: `nums[1:3]`, `nums[:2]`, `nums[-2:]`.
- `*` is a jseek extension: like `#` but also iterates object members.

Measured on 500 records (Apple M4 Pro): `users.#.name` **~19 µs / 33 KB**
vs gjson ~64 µs / 151 KB (**~3.3× faster, ~4.6× less memory**); filtered
all-matches ~37 µs vs ~62 µs. Semantics are cross-checked against gjson by
`TestQueryMatchesGJSON` / `FuzzQueryMatchesGJSON` (bench/) and against jq
byte-for-byte for the advanced forms; `FuzzQueryAdvancedNoPanic` guards the
recursive/slice/compound paths.

### Formatting (`Compact` / `Indent`)

Re-serialize JSON without decoding it into Go values — the primitives behind
`jseek -c` / `jseek -i`. Both validate structure, never mutate the input, and
match `encoding/json.Compact` / `json.Indent` byte-for-byte (guarded by
`FuzzFormatMatchesStdlib`):

```go
min, _ := jseek.Compact(data)        // strip insignificant whitespace
pretty, _ := jseek.Indent(data, "  ") // 2-space pretty-print
```

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
dec := jseek.NewDecoder(resp.Body)       // arrays / NDJSON / mixed values
defer dec.Close()                        // return the pooled read buffer
err := dec.ForEach(func(elem []byte) error {
    name, _ := jseek.GetString(elem, "user", "name")
    // ... process one record; elem is valid only for this call
    return nil
})
```

`NewDecoder` auto-detects the stream shape: `[...]` arrays, whitespace-separated
values, **and NDJSON** — after the first value, a following newline switches it
to line mode automatically, so the generic decoder is now the fastest NDJSON
Reader path too (~1.44 ms vs gjson ~1.74 ms on 5000 log lines). `NewNDJSONDecoder`
forces line mode from the start. Read buffers are pooled (`sync.Pool`): call
`Close` (or reuse via `Reset`) so per-request construction adds ~0 allocations
instead of a fresh 256 KiB bufio. Set `Decoder.MaxValue` to cap per-element size
and reject hostile input with `ErrTooLarge`.

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
- `FuzzValidMatchesStdlib` — `Valid` agrees with `encoding/json.Valid` on
  every input, valid or malformed.
- `FuzzCountMatchesStdlib` — `Count` equals the cardinality of a full stdlib
  decode on valid JSON (duplicate-key inputs excluded, per the first-wins
  contract).

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
dotted-path and JSON Pointer syntaxes (with `.#` count paths), contextual
errors, zero-allocation `Valid` and `Count`, gjson-compatible wildcard/filter
queries (`Query`/`QueryFirst`), and memory-bounded streaming
(`Decoder`/`StreamBytes`/`StreamNDJSON`). Navigation includes FASS equal-size
array strides and a SWAR wide-key compare for long keys; scanning includes
portable SWAR plus shipped AVX2/NEON kernels (opt out with `-tags purego`).
The SWAR whitespace mask uses an exact zero-byte detector, closing a
borrow-chain false-positive class that could mis-skip bytes adjacent to
spaces (guarded by exhaustive property tests and differential fuzzing).

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
| Large doc, deep indexed (2 Gets) | **3.14 µs** / 0 B | 256 µs / 0 B | 97 µs / 16 B |
| Same via `GetFields` | **1.62 µs** / 0 B | — | — |
| Large doc, full ArrayEach + 2 fields/elem | **93.5 µs** / 0 B | 240 µs / 0 B | 310 µs / 188 KB |
| Large doc, `EachArrayFields` (2 fields/elem) | **80.3 µs** / 0 B | — | — |
| Multi-path (6 fields, `EachKey`) | **45.1 µs** / 0 B | — | 219 µs / 536 B |
| Cold Index + 12 fields (STE) | **70.1 µs** / 0 B | — | 497 µs / 1.2 KB |
| Stateless 12 fields (FASS) | 80.0 µs / 0 B | — | 497 µs / 1.2 KB |
| IndexTape, 12 fields | **1.34 µs** / 0 B | — | — |
| Deep access + topology (+ tape) | **0.27 µs** (**0.12 µs**) | — | — |
| GitHub 7 fields (cold FASS) | **5.99 µs** / 0 B | 150 µs / 0 B | 57 µs / 664 B |

`jseek` leads the lazy class on single-field, multi-path, deep homogeneous arrays
(FASS), STE Stage-1, topology stride, and IndexTape reuse — all **zero allocation**. Deep `users[250]` (~3.1 µs) and GitHub-style issue arrays (~6.0 µs cold) are both
ahead of sonic's arm64 path; cold Index multi-get now leads both FASS re-scan and sonic. **Every NDJSON path now beats gjson**: in-memory
`StreamNDJSONEach` ~1.11 ms, and the generic `NewDecoder` (auto-detecting NDJSON) ~1.44 ms vs gjson ~1.74 ms / 5000 lines — the former
generic-framing weak spot is closed. See
[`docs/BENCHMARKS.md`](docs/BENCHMARKS.md) for full boundaries and methodology.
