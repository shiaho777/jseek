// Command jseek is a fast JSON query and formatting tool built on the jseek
// lazy-extraction engine. It is a single static binary with zero runtime
// dependencies.
//
// Usage:
//
//	jseek [flags] [path] [file ...]
//
// Examples:
//
//	jseek user.name config.json            # extract a field
//	jseek 'users.#.name' api.json          # every element's name
//	jseek 'users.#(age>30)#.email' api.json  # filtered, all matches
//	jseek -c big.json                      # minify
//	jseek -i big.json                      # pretty-print
//	jseek -n 'latency_ms' logs.ndjson      # stream NDJSON, one value per line
//	jseek -v untrusted.json                # validate (exit 0/1)
//	cat api.json | jseek 'users[0].name'   # stdin
//
// Path syntax is jseek's: dotted keys, [N] indices, "#" array wildcard /
// count, "#(key op value)" filters ("#(...)#" for all matches), "*" wildcard
// over array elements or object members.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/shiaho777/jseek"
)

// version is overridden at release build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	var (
		compact  = flag.Bool("c", false, "compact (minify) JSON")
		indent   = flag.Bool("i", false, "pretty-print JSON (2-space indent)")
		ndjson   = flag.Bool("n", false, "treat input as NDJSON; apply path to each line")
		parallel = flag.Int("p", 1, "with -n: number of parallel workers (order preserved)")
		raw      = flag.Bool("r", false, "raw output: print strings without quotes")
		validate = flag.Bool("v", false, "validate JSON; exit 0 if valid, 1 otherwise")
		strict   = flag.Bool("strict", false, "with -v: also reject invalid UTF-8, lone surrogates, duplicate keys")
		count    = flag.Bool("C", false, "print the count of array elements / object members at path")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("jseek " + version)
		return
	}

	args := flag.Args()
	path := ""
	var files []string
	if len(args) > 0 {
		// First non-flag arg is the path unless a mode flag consumes input
		// without a path. Heuristic: if -c/-i/-v are set and the first arg
		// looks like an existing file, treat all args as files.
		if (*compact || *indent || *validate) && !*count && fileExists(args[0]) {
			files = args
		} else {
			path = args[0]
			files = args[1:]
		}
	}

	if *validate {
		os.Exit(runValidate(files, *strict))
	}

	if len(files) == 0 {
		files = []string{"-"}
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	for _, f := range files {
		r, closeFn, err := openInput(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			os.Exit(2)
		}
		var rerr error
		switch {
		case *compact:
			rerr = runCompact(r, w)
		case *indent:
			rerr = runIndent(r, w)
		case *ndjson:
			if *parallel > 1 {
				rerr = runNDJSONParallel(r, w, path, *raw, *count, *parallel)
			} else {
				rerr = runNDJSON(r, w, path, *raw, *count)
			}
		default:
			rerr = runQuery(r, w, path, *raw, *count)
		}
		closeFn()
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "jseek:", rerr)
			os.Exit(1)
		}
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `jseek — fast JSON query & formatting (zero dependencies)

Usage: jseek [flags] [path] [file ...]   (reads stdin when no file given)

Flags:
  -c          compact (minify) JSON
  -i          pretty-print JSON
  -n          NDJSON mode: apply path to each line
  -p N        with -n: N parallel workers (order preserved); pays off for
              compute-heavy per-line queries, not trivial field reads
  -r          raw output (strings without quotes)
  -v          validate JSON (exit 0 valid / 1 invalid)
  -strict     with -v: reject invalid UTF-8, lone surrogates, duplicate keys
  -C          print count at path instead of values
  -version    print version

Path syntax:
  user.name            dotted keys
  users[0].name        array index
  users.#.name         every element ("#" wildcard)
  users.#              array length
  users.#(age>30).name first match of a filter
  users.#(age>30)#.name all matches ("#(...)#")
  users.#(a>1 && b<2)#  compound filters (&& / ||)
  ..name               recursive descent (any depth)
  nums[1:3]            array slice (negatives from end: [-2:])
  meta.*               object members ("*" extension)

Examples:
  jseek user.name config.json
  jseek 'users.#(active==true)#.email' api.json
  jseek -n -p 8 'latency_ms' logs.ndjson
  jseek -c big.json | jseek -v -strict
`)
}

func fileExists(s string) bool {
	if s == "-" {
		return false
	}
	st, err := os.Stat(s)
	return err == nil && !st.IsDir()
}

func openInput(f string) (io.Reader, func(), error) {
	if f == "-" {
		return os.Stdin, func() {}, nil
	}
	fh, err := os.Open(f)
	if err != nil {
		return nil, nil, err
	}
	return fh, func() { fh.Close() }, nil
}

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func runValidate(files []string, strict bool) int {
	if len(files) == 0 {
		files = []string{"-"}
	}
	check := jseek.Valid
	if strict {
		check = jseek.ValidStrict
	}
	bad := 0
	for _, f := range files {
		r, closeFn, err := openInput(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 2
		}
		data, rerr := readAll(r)
		closeFn()
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "jseek:", rerr)
			return 2
		}
		if check(data) {
			fmt.Printf("%s: valid\n", displayName(f))
		} else {
			fmt.Printf("%s: invalid\n", displayName(f))
			bad++
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func displayName(f string) string {
	if f == "-" {
		return "<stdin>"
	}
	return f
}

func runCompact(r io.Reader, w *bufio.Writer) error {
	data, err := readAll(r)
	if err != nil {
		return err
	}
	out, err := jseek.Compact(data)
	if err != nil {
		return err
	}
	w.Write(out)
	w.WriteByte('\n')
	return nil
}

func runIndent(r io.Reader, w *bufio.Writer) error {
	data, err := readAll(r)
	if err != nil {
		return err
	}
	out, err := jseek.Indent(data, "  ")
	if err != nil {
		return err
	}
	w.Write(out)
	w.WriteByte('\n')
	return nil
}

// runQuery loads the whole input and applies the path. With no path it
// pretty-prints (the jq "." default).
func runQuery(r io.Reader, w *bufio.Writer, path string, raw, count bool) error {
	data, err := readAll(r)
	if err != nil {
		return err
	}
	if path == "" || path == "." {
		out, err := jseek.Indent(data, "  ")
		if err != nil {
			return err
		}
		w.Write(out)
		w.WriteByte('\n')
		return nil
	}
	if count {
		n, err := jseek.Count(data, jseek.ParseDotPath(path)...)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, n)
		return nil
	}
	results, err := jseek.QueryTyped(data, path)
	if err != nil {
		return err
	}
	for _, res := range results {
		writeTyped(w, res, raw)
	}
	return nil
}

// processLine renders one NDJSON element into buf according to the mode.
func processLine(buf *bytes.Buffer, elem []byte, path string, raw, count bool) error {
	if path == "" || path == "." {
		out, err := jseek.Compact(elem)
		if err != nil {
			return err
		}
		buf.Write(out)
		buf.WriteByte('\n')
		return nil
	}
	if count {
		n, err := jseek.Count(elem, jseek.ParseDotPath(path)...)
		if err != nil {
			return nil // missing path on a line: skip
		}
		fmt.Fprintln(buf, n)
		return nil
	}
	results, err := jseek.QueryTyped(elem, path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(buf)
	for _, res := range results {
		writeTyped(bw, res, raw)
	}
	return bw.Flush()
}

// runNDJSON streams line-by-line with bounded memory.
func runNDJSON(r io.Reader, w *bufio.Writer, path string, raw, count bool) error {
	dec := jseek.NewNDJSONDecoder(r)
	defer dec.Close()
	var buf bytes.Buffer
	return dec.ForEach(func(elem []byte) error {
		buf.Reset()
		if err := processLine(&buf, elem, path, raw, count); err != nil {
			return err
		}
		_, err := w.Write(buf.Bytes())
		return err
	})
}

// runNDJSONParallel processes NDJSON with n workers in a read/process/write
// pipeline that preserves input order. Reading and writing stay sequential
// (order + single stdout); the per-line query work runs concurrently.
func runNDJSONParallel(r io.Reader, w *bufio.Writer, path string, raw, count bool, n int) error {
	if n < 1 {
		n = 1
	}
	if n == 1 {
		return runNDJSON(r, w, path, raw, count)
	}
	type job struct {
		seq  int
		line []byte
	}
	type result struct {
		seq int
		out []byte
		err error
	}
	jobs := make(chan job, n*4)
	results := make(chan result, n*4)

	// Workers: process lines concurrently.
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			for j := range jobs {
				buf.Reset()
				err := processLine(&buf, j.line, path, raw, count)
				out := append([]byte(nil), buf.Bytes()...)
				results <- result{seq: j.seq, out: out, err: err}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	// Reader: split input into lines, dispatch in order.
	readErr := make(chan error, 1)
	go func() {
		defer close(jobs)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
		seq := 0
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			cp := append([]byte(nil), line...)
			jobs <- job{seq: seq, line: cp}
			seq++
		}
		readErr <- sc.Err()
	}()

	// Writer: reorder by sequence and emit.
	pending := make(map[int]result)
	next := 0
	var writeErr error
	for res := range results {
		if res.err != nil && writeErr == nil {
			writeErr = res.err
		}
		pending[res.seq] = res
		for {
			p, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if writeErr == nil && len(p.out) > 0 {
				if _, err := w.Write(p.out); err != nil && writeErr == nil {
					writeErr = err
				}
			}
			next++
		}
	}
	if err := <-readErr; err != nil && writeErr == nil {
		writeErr = err
	}
	return writeErr
}

// writeTyped prints one typed result. Strings are re-quoted for valid JSON
// output unless -r is set; numbers/bools/null/containers print verbatim.
func writeTyped(w *bufio.Writer, res jseek.QueryResult, raw bool) {
	if res.Type == jseek.String {
		if raw {
			w.Write(res.Raw)
		} else {
			writeQuoted(w, res.Raw)
		}
	} else {
		w.Write(res.Raw)
	}
	w.WriteByte('\n')
}

// writeQuoted emits v as a JSON string, escaping as needed.
func writeQuoted(w *bufio.Writer, v []byte) {
	w.WriteByte('"')
	for _, c := range v {
		switch c {
		case '"':
			w.WriteString(`\"`)
		case '\\':
			w.WriteString(`\\`)
		case '\n':
			w.WriteString(`\n`)
		case '\r':
			w.WriteString(`\r`)
		case '\t':
			w.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(w, `\u%04x`, c)
			} else {
				w.WriteByte(c)
			}
		}
	}
	w.WriteByte('"')
}