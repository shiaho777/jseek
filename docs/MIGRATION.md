# Migrating from gjson

`jseek/gjsoncompat` is a drop-in replacement for
[`github.com/tidwall/gjson`](https://github.com/tidwall/gjson) backed by jseek's
lazy-extraction engine. For the common read API, switching engines is a
**one-line import change**:

```diff
- import "github.com/tidwall/gjson"
+ import gjson "github.com/shiaho777/jseek/gjsoncompat"
```

Call sites that use `gjson.Get`, `gjson.GetBytes`, `gjson.Parse`,
`gjson.GetMany`, `gjson.GetManyBytes`, `gjson.Valid`, and the `Result` accessors
(`.String()`, `.Int()`, `.Uint()`, `.Float()`, `.Bool()`, `.Array()`, `.Map()`,
`.Value()`, `.Exists()`, `.IsArray()`, `.IsObject()`) compile unchanged.

```go
import gjson "github.com/shiaho777/jseek/gjsoncompat"

doc := `{"users":[{"id":1,"name":"Ada"},{"id":2,"name":"Bob"}]}`
name := gjson.Get(doc, "users.0.name").String() // "Ada"
n    := gjson.Get(doc, "users.#").Int()          // 2
ok   := gjson.Valid(doc)                         // true
```

## What is preserved

- **`Result.Type` numbering** matches gjson exactly: `Null=0, False=1,
  Number=2, String=3, True=4, JSON=5`.
- **Single-value paths** (`a.b.c`, `users.0.name`, `users[0]`) return the first
  match, like gjson.
- **Terminal `#` counts** (`users.#`) return a `Number`, like gjson.
- **Multi-match paths** (`users.#.name`, wildcards) return a JSON array of
  matches, like gjson.
- **Numeric object keys**: a bare numeric segment on an object (e.g. `"0"` on
  `{"0":…}`) resolves as an object key, matching gjson (jseek's native dotted
  parser treats bare numerics as array indices; the compat layer bridges this).
- **Lenient number overflow**: `1e1000` parses to `+Inf`, matching gjson's
  lenient float parser.

## What is not implemented (returns a not-found `Result`, never panics)

- gjson **modifiers** (`@reverse`, `@pretty`, `@ugly`, `@valid`, `@this`, …).
- gjson **query/filter syntax** beyond jseek's own (`#(age>30)` filters *are*
  supported via jseek's query engine; gjson-specific comparison modifiers are
  not).
- gjson's quirky behavior on **scalar roots with arbitrary keys**. The compat
  layer targets plain object-key and path lookups on object/array documents —
  the real migration case.

If your code relies on modifiers, keep those call sites on gjson and migrate the
rest, or translate them to jseek's native query syntax.

## Correctness

The compat layer is differential-tested against real gjson in the `bench`
module:

- `TestGjsonCompat*` — table tests over the common API surface.
- `FuzzGjsonCompatSinglePath` — differential fuzzing of single-key lookups on
  object documents; **8.5M+ executions** with zero divergence after the
  numeric-key and overflow fixes.

Run them yourself:

```bash
cd bench
go test -run TestGjsonCompat -count=1
go test -run '^$' -fuzz FuzzGjsonCompatSinglePath -fuzztime 30s
```

## Why migrate

jseek's lazy walker skips every subtree a path does not touch, so extraction
from large payloads avoids decoding the whole document. See
[BENCHMARKS.md](BENCHMARKS.md) for same-machine A/B numbers against gjson,
jsonparser, sonic, and simdjson-go, including the cases where jseek does *not*
win.