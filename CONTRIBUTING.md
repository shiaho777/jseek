# Contributing to jseek

Thanks for your interest in jseek. This guide covers how to build, test, and
propose changes while preserving the project's core guarantees.

## Prerequisites

- Go 1.23 or newer.
- No external dependencies for the main module. The `bench/` module pulls in
  `jsonparser` and `gjson` for comparison only.

## Project layout

jseek is a single Go package at the module root. The `bench/` subdirectory is a
separate module so benchmark-only dependencies never leak into consumers.

Read `ARCHITECTURE.md` first — it explains the layers and the invariants you
must not break.

## Development loop

```sh
go build ./...                 # compile
go vet ./...                   # static checks
go test ./...                  # unit tests + runnable examples
go test -race ./...            # concurrency checks (Document is read-shared)
go test -tags jseeksafe ./...    # the allocation-safe, no-unsafe build
```

Benchmarks live in the `bench/` module:

```sh
cd bench
go test -bench=. -benchmem
```

See [`BENCHMARKS.md`](BENCHMARKS.md) for the full results, methodology, and the
honest record of where `jseek` wins and loses. When a change affects performance,
update those numbers and include before/after benchstat output in your PR.

## The five invariants

Every change must preserve these (see `ARCHITECTURE.md` for detail):

1. The read path allocates nothing beyond documented exceptions.
2. Input bytes are never mutated.
3. The stateless and indexed scan engines must agree on valid JSON.
4. New JSON-interpreting features ship with a differential fuzz test.
5. The contract is valid UTF-8 JSON; malformed input must never panic.

## Adding a feature

1. Implement it in a focused file matching the layer it belongs to.
2. Add unit tests and, if it interprets JSON, a `Fuzz*` test that compares
   against `encoding/json` or an existing jseek path.
3. Add a runnable `Example` in `example_test.go` if it is part of the public API.
4. If it touches a hot path, add or update a benchmark and include before/after
   numbers in your PR.
5. Update `README.md` and, if structural, `ARCHITECTURE.md`.

## Running fuzz tests

```sh
# Run a single target locally for a minute.
go test -run=x -fuzz=FuzzGetAgainstStdlib -fuzztime=60s

# Available targets:
#   FuzzGetAgainstStdlib        Get vs encoding/json
#   FuzzDocumentMatchesGet      indexed engine vs stateless Get
#   FuzzEachKeyMatchesGet       multi-path vs repeated Get
#   FuzzSetAgainstStdlib        Set vs encoding/json
#   FuzzDeleteAgainstStdlib     Delete vs encoding/json
#   FuzzStreamMatchesArrayEach  streaming vs ArrayEach
```

If a fuzzer finds a crash, Go writes the input to `testdata/fuzz/<Target>/`.
**Commit that file** with your fix — it becomes a permanent regression test.

## SIMD / assembly contributions

Hardware SIMD kernels are welcome but held to a high bar:

- Guard them with build tags (`//go:build amd64 && !purego`) and always keep the
  pure-Go/SWAR path as the `purego` fallback.
- They must pass the full differential fuzz suite on the target architecture in
  CI before merge.
- Include a benchmark showing the win on representative payloads.

## Commit and PR conventions

- Keep PRs focused; one logical change per PR.
- Describe what you changed, what you tested, and any benchmark deltas.
- Make sure CI is green on both amd64 and arm64.
