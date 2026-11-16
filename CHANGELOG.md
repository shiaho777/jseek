# Changelog

All notable changes to jseek are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow semver once
tagged releases begin.

## [Unreleased]

### Added
- **Advanced query syntax**: recursive descent `..key`, array slices
  `[start:end]` (negative bounds from end), compound filters `#(a && b || c)#`
  with `&&`-over-`||` precedence. Output verified byte-identical to jq;
  guarded by `FuzzQueryAdvancedNoPanic`.
- **`ValidStrict`**: json/v2-style safety validation — rejects invalid UTF-8,
  lone/unpaired surrogates, and duplicate object keys. Strict subset of
  `Valid`; zero-alloc on scalars/arrays. CLI: `jseek -v -strict`.
- **CLI parallel NDJSON (`-p N`)**: order-preserving read/process/write
  pipeline; ~1.8× at 4 workers on compute-heavy per-line queries (124 MB
  fixture). Documented boundary: trivial field reads are I/O-bound, default 1.
- **`jseek` CLI** (`cmd/jseek`): single static binary, zero runtime
  dependencies — path/wildcard/filter queries, NDJSON streaming with bounded
  memory, validation, compact and pretty-print. Measured 15–75× faster than
  jq 1.7.1 on 7.5–17.6 MB real files with byte-identical output.
- **`Valid`**: zero-allocation JSON syntax validator, agrees with
  `encoding/json.Valid` on all inputs (~1 GB/s).
- **`Count` / `CountSeg` / `.#` count paths**: zero-allocation container
  cardinality.
- **`Query` / `QueryTyped` / `QueryFirst` / `Document.Query`**:
  gjson-compatible wildcard and filter queries (`users.#.name`,
  `users.#(age>30)#.email`); ~3.2× faster than gjson with ~4.6× less memory
  on 500-record harvests.
- **`Compact` / `Indent`**: re-serialization matching `encoding/json`
  byte-for-byte.
- **`Decoder.Close`** and pooled read buffers: per-request NDJSON decoding
  adds ~0 steady-state allocations (was 263 KB per construction).
- Release workflow: tag-triggered cross-platform binaries
  (linux/darwin/windows × amd64/arm64) with SHA256 sums.
- Makefile with the full CI-equivalent check matrix.
- Differential fuzzers: `FuzzValidMatchesStdlib`, `FuzzCountMatchesStdlib`,
  `FuzzFormatMatchesStdlib`, `FuzzQueryMatchesGJSON` (bench module).

### Changed
- `NewDecoder` auto-detects NDJSON after the first value and switches to line
  mode: the generic Reader path went from 2.17 ms (slower than gjson's
  1.73 ms) to ~1.44 ms (~1.2× faster). Every NDJSON path now beats gjson.
- `GetPath` / `GetPointer` resolve simple paths with zero allocation
  (stack-resident segment parsing; deep/escaped paths fall back).
- `GetString` decodes into one right-sized buffer (~12% faster on long
  strings).
- Long object keys (≥16 bytes) use a SWAR wide scan in `scanKey`
  (~1.6× on 33-byte keys).
- `parseInt` replaces per-digit division with a digit-count overflow bound
  (~15% faster on 19-digit values).
- CI gates extended: combined `purego jseeksafe` build, all 14 differential
  seed corpora, and the bench module's gjson/jsonparser/sonic compatibility
  tests are now required (`ci success` aggregates them).

### Fixed
- **SWAR zero-byte borrow-chain false positives**: the classic
  `(v-LSB)&^v&MSB` trick misread bytes adjacent to spaces (e.g. `'!'`) as
  whitespace, which could let trailing garbage pass validation and mis-skip
  bytes during navigation. Masks trusted without a per-byte re-check now use
  an exact detector (`zeroByteMaskExact`); guarded by exhaustive property
  tests and differential fuzzing. Found by the new `FuzzValidMatchesStdlib`.

### Verified
- Full matrix green: unit + race + `jseeksafe` + `purego` + combined builds.
- All 15 differential fuzz targets pass multi-million-execution campaigns.
- CLI output byte-identical to jq on 50 000-line wildcard harvests and
  minification of a 7.5 MB document.