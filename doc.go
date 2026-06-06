// Package jseek is a high-performance, zero-allocation JSON value extractor for Go.
//
// jseek navigates raw JSON bytes lazily, skipping entire subtrees it does not
// need, rather than fully decoding a document into Go values (like
// encoding/json) or materializing a full parse tape up front. This makes it
// ideal for the common real-world case of pulling a handful of fields out of
// large, dynamic JSON payloads — and, with the indexed and columnar APIs below,
// for querying the same data repeatedly at near-native-slice speed.
//
// # Reading values
//
//	data := []byte(`{"user":{"name":"Ada","followers":42}}`)
//	name, _ := jseek.GetString(data, "user", "name")  // "Ada"
//	n, _ := jseek.GetInt(data, "user", "followers")    // 42
//
// Array elements use bracketed indices: jseek.GetString(data, "users", "[0]",
// "name"). Generic accessors At[T] and Or[T] cover string/bool/int64/float64.
// Paths may also be written as dotted strings (GetPath, "a.b[0].c") or RFC 6901
// JSON Pointers (GetPointer, "/a/b/0/c"). ArrayEach and ObjectEach iterate
// containers; EachKey / GetMany read several paths in a single pass.
//
// # Index once, query many
//
// For repeated queries on one document, Index builds a reusable structural
// index so each query navigates a compact index instead of re-scanning bytes;
// IndexTape adds O(1) subtree skipping for deep access. Pin caches a path's
// learned trajectory for near-direct-address repeat reads. These are the basis
// of jseek's largest speedups (see docs/BENCHMARKS.md).
//
// # Columnar analytics
//
// Transpose / TransposeInt / TransposeFloat / TransposeString / TransposeBool
// extract a field from a batch of records into a contiguous native slice in one
// pass, so repeated aggregation over a batch runs as a plain slice scan with no
// further JSON parsing.
//
// # Mutation and streaming
//
// Set and Delete return a new document with a value replaced or removed (the
// input is never mutated). Each resolves to a single contiguous edit located in
// one downward pass, so it allocates once regardless of path depth. AppendSet
// and AppendDelete write that result into a caller-provided buffer, so a hot
// loop reusing one scratch buffer mutates with amortized zero allocation.
// Decoder streams a top-level array or NDJSON from an io.Reader with bounded
// memory; StreamBytes does the same over an in-memory slice with zero copy.
//
// # Scanning core
//
// The byte-scanning hot loops use SWAR (eight bytes per 64-bit word) behind a
// dispatch seam, so hand-written SIMD kernels (AVX2/AVX-512 on amd64, NEON on
// arm64) can be added later without changing any caller. Build with the
// "purego" tag to force the portable path, or "jseeksafe" to drop the unsafe
// zero-copy string view.
//
// # Concurrency
//
// All package-level read functions are safe for concurrent use on the same
// input, since jseek never mutates the input. A Document's read methods are
// concurrency-safe; its mutating methods (Reset, WithTape, Free, Pin) are not.
// Decoder and Pinned are not safe for concurrent use.
package jseek
