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
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/shiaho777/jseek"
)

// version is overridden at release build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	var (
		compact  = flag.Bool("c", false, "compact (minify) JSON")
		indent   = flag.Bool("i", false, "pretty-print JSON (2-space indent)")
		ndjson   = flag.Bool("n", false, "treat input as NDJSON; apply path to each line")
		raw      = flag.Bool("r", false, "raw output: print strings without quotes")
		validate = flag.Bool("v", false, "validate JSON; exit 0 if valid, 1 otherwise")
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
		os.Exit(runValidate(files))
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
			rerr = runNDJSON(r, w, path, *raw, *count)
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
  -r          raw output (strings without quotes)
  -v          validate JSON (exit 0 valid / 1 invalid)
  -C          print count at path instead of values
  -version    print version

Path syntax:
  user.name            dotted keys
  users[0].name        array index
  users.#.name         every element ("#" wildcard)
  users.#              array length
  users.#(age>30).name first match of a filter
  users.#(age>30)#.name all matches ("#(...)#")
  meta.*               object members ("*" extension)

Examples:
  jseek user.name config.json
  jseek 'users.#(active==true)#.email' api.json
  jseek -n 'latency_ms' logs.ndjson
  jseek -c big.json | jseek -v
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

func runValidate(files []string) int {
	if len(files) == 0 {
		files = []string{"-"}
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
		if jseek.Valid(data) {
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

// runNDJSON streams line-by-line with bounded memory.
func runNDJSON(r io.Reader, w *bufio.Writer, path string, raw, count bool) error {
	dec := jseek.NewNDJSONDecoder(r)
	defer dec.Close()
	return dec.ForEach(func(elem []byte) error {
		if path == "" || path == "." {
			out, err := jseek.Compact(elem)
			if err != nil {
				return err
			}
			w.Write(out)
			w.WriteByte('\n')
			return nil
		}
		if count {
			n, err := jseek.Count(elem, jseek.ParseDotPath(path)...)
			if err != nil {
				return nil // missing path on a line: skip
			}
			fmt.Fprintln(w, n)
			return nil
		}
		results, err := jseek.QueryTyped(elem, path)
		if err != nil {
			return err
		}
		for _, res := range results {
			writeTyped(w, res, raw)
		}
		return nil
	})
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