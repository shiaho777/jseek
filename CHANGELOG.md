# Changelog

All notable changes to jseek are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow semver once
tagged releases begin.

## [Unreleased]

### Added
- **Persistent structural index — zero-parse data substrate**: `Document.Persist`,
  `Open`, `View`, `ViewTrusted`, `VerifyIndex`, `IndexSize`. The pointer-free
  index is sealed to disk once and re-opened by any process with no re-scan;
  the canonical mmap pattern (verify once, then `ViewTrusted`) opens in ~126 ns
  / 128 B vs ~19.5 µs / 109 KB to rebuild (~155× / ~850×). Cross-process demo
  sustains 100k opens+queries at ~229 ns. Payload bound by a word-parallel
  64-bit hash (~6.7 GB/s); zero-copy reinterpretation in the default build,
  safe decode under `jseeksafe`. Every index offset is bounds-checked.
- **Self-contained sealed stores**: `Seal`, `OpenSealed`, `ViewSealed`,
  `ViewSealedTrusted`, `VerifySealed` pack payload+index into one portable file.
- **mmap store handle**: `OpenStore` / `Store.View` / `Store.Close` — read-only
  mapping, verified once, concurrent-safe O(1) views (unix mmap; other
  platforms fall back to a one-time read).
- **CLI store modes**: `jseek -seal in.json out.jstore` and
  `jseek -open out.jstore 'path'` expose the substrate from the terminal.
- **FuzzPersistRoundTrip**: persist→open round-trip + corrupt-rejection fuzz.
- **NDJSON random-access store**: `SealNDJSON`, `OpenNDJSONStore`,
  `NDJSONStore.Record`/`RecordInto`/`Raw`/`Each`/`Len`/`Close`, `AppendNDJSON`.
  Per-record offset table + sealed indexes give O(1) record jumps (~45 ns vs
  ~69 µs sequential, ~1500×) and incremental append. CLI: `-seal-n` / `-open-n
  -at N` / `-append-n`. Guarded by `FuzzNDJSONStoreRoundTrip`.
- **Zero-allocation reuse**: `ViewInto` re-points an existing Document for
  alloc-free hot loops.
- **Incremental index patch (single-document write path)**: `PatchSet` /
  `PatchDelete` update a Document's index without a full re-scan (prefix kept,
  suffix shifted, edit region re-scanned). `Store.Set` / `Store.Delete` expose
  field-level writes on a sealed store. Guarded by `FuzzPatchMatchesRebuild`
  (5.5M execs). Honest note: modest speed gain (~0–17%) because the payload copy
  dominates; the value is enabling writes + ~45% less index memory on front
  edits, not a dramatic speedup.
- **WAL overlay store — genuinely cheap writes**: `WALStore` (`OpenWAL`/`NewWAL`,
  `Set`/`Delete`/`Get`/`Exists`/typed getters, `Pending`/`Compact`/`Close`).
  Append-only edit log over an immutable sealed base: a write is ~180 ns / ~300 B
  vs ~82 µs / 344 KB for a full rewrite (~450×), and **independent of document
  size** (100→50 000 records all ~160–186 ns). Optional WAL file for crash-safe
  replay; `Compact` folds the log into a new base. CLI: `-wal` +
  `-wset`/`-wdel`/`-wget`/`-wcompact`. Guarded by `FuzzWALMatchesMaterialize`.
- **Generation-keyed materialization cache**: repeated reads of an
  overlay-dirtied subtree are O(1) (~93 ns / 24 B); the cache rebuilds only when
  the edit set changes (append/compact bumps a generation counter).
- **Subtree-local resolution (breaks the first-dirty-read boundary)**: a dirty
  read now touches only the requested subtree + edits inside it, never the whole
  document. First dirty read of an element in a 50 000-record array: ~11 µs /
  1.4 KB vs ~103.7 ms / 9.46 MB for full materialization (~9,400× / ~6,700×);
  allocation is flat regardless of document size. Locates the base subtree via
  the structural index; applies only descendant edits relative to it. Guarded by
  `FuzzWALMatchesMaterialize`.
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