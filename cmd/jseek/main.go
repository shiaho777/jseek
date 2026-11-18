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
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
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
		seal     = flag.Bool("seal", false, "build a self-contained zero-parse store: jseek -seal in.json out.jstore")
		open     = flag.Bool("open", false, "query a sealed store with zero re-parsing: jseek -open store.jstore 'path'")
		sealN    = flag.Bool("seal-n", false, "build a random-access NDJSON store: jseek -seal-n in.ndjson out.njstore")
		openN    = flag.Bool("open-n", false, "query an NDJSON store: jseek -open-n out.njstore 'path' [-at N]")
		appendN  = flag.Bool("append-n", false, "append records: jseek -append-n store.njstore new.ndjson")
		at       = flag.Int("at", -1, "with -open-n: select a single record by index (0-based)")
		query    = flag.Bool("q", false, "query an NDJSON store: jseek -q store.njstore [-where cond] [-select f1,f2] [-agg field] [-group key] [-limit N]")
		where    = flag.String("where", "", "with -q: filter, e.g. 'latency_ms > 100 && status == \"error\"'")
		sel      = flag.String("select", "", "with -q: comma-separated projection fields (default: whole record)")
		agg      = flag.String("agg", "", "with -q: aggregate a numeric field (count/sum/avg/min/max)")
		group    = flag.String("group", "", "with -q and -agg: group by this field")
		limit    = flag.Int("limit", 0, "with -q: max records to emit (0 = unlimited)")
		wal      = flag.String("wal", "", "WAL log file for cheap writes (used with -wset/-wdel/-wget/-wcompact)")
		wset     = flag.Bool("wset", false, "cheap write: jseek -wal log -wset store.jstore 'path' 'jsonValue'")
		wdel     = flag.Bool("wdel", false, "cheap delete: jseek -wal log -wdel store.jstore 'path'")
		wget     = flag.Bool("wget", false, "overlay-aware read: jseek -wal log -wget store.jstore 'path'")
		wcompact = flag.Bool("wcompact", false, "fold WAL into a new base: jseek -wal log -wcompact store.jstore out.jstore")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("jseek " + version)
		return
	}

	// Store modes have their own argument shapes; handle before generic parsing.
	if *seal {
		os.Exit(runSeal(flag.Args()))
	}
	if *open {
		os.Exit(runOpenStore(flag.Args(), *raw, *count))
	}
	if *sealN {
		os.Exit(runSealNDJSON(flag.Args()))
	}
	if *openN {
		os.Exit(runOpenNDJSON(flag.Args(), *raw, *count, *at))
	}
	if *wset || *wdel || *wget || *wcompact {
		os.Exit(runWAL(flag.Args(), *wal, *wset, *wdel, *wget, *wcompact, *raw))
	}
	if *appendN {
		os.Exit(runAppendNDJSON(flag.Args()))
	}
	if *query {
		os.Exit(runQueryNDJSON(flag.Args(), *where, *sel, *agg, *group, *limit, *raw))
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
  -seal       build a zero-parse store: jseek -seal in.json out.jstore
  -open       query a store with no re-parsing: jseek -open out.jstore 'path'
  -seal-n     build a random-access NDJSON store: jseek -seal-n in.ndjson out.njstore
  -open-n     query an NDJSON store: jseek -open-n out.njstore 'path' [-at N]
  -append-n   append records: jseek -append-n store.njstore new.ndjson
  -at N       with -open-n: select one record by index (0-based)
  -wal FILE   WAL log for cheap writes (with -wset/-wdel/-wget/-wcompact)
  -wset       cheap write: jseek -wal log -wset store.jstore 'path' 'jsonValue'
  -wdel       cheap delete: jseek -wal log -wdel store.jstore 'path'
  -wget       overlay read: jseek -wal log -wget store.jstore 'path'
  -wcompact   fold WAL into new base: jseek -wal log -wcompact store.jstore out.jstore
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
  jseek -seal big.json big.jstore        # build once
  jseek -open big.jstore 'users.#.name'  # query forever, zero re-parse
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

// runSeal builds a self-contained store: jseek -seal in.json out.jstore
func runSeal(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "jseek: -seal needs <input.json> <output.jstore>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	if !jseek.Valid(data) {
		fmt.Fprintln(os.Stderr, "jseek: input is not valid JSON")
		return 1
	}
	out, err := os.Create(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	defer out.Close()
	bw := bufio.NewWriter(out)
	if err := jseek.Seal(bw, data); err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	if err := bw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	st, _ := out.Stat()
	fmt.Fprintf(os.Stderr, "sealed %s -> %s (%d bytes payload, %d bytes store)\n",
		args[0], args[1], len(data), st.Size())
	return 0
}

// runOpenStore queries a sealed store with zero re-parsing:
// jseek -open store.jstore 'path'   (no path = pretty-print payload)
func runOpenStore(args []string, raw, count bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "jseek: -open needs <store.jstore> [path]")
		return 2
	}
	s, err := jseek.OpenStore(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	defer s.Close()
	path := ""
	if len(args) > 1 {
		path = args[1]
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	data := s.Data()
	if path == "" || path == "." {
		out, err := jseek.Indent(data, "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		w.Write(out)
		w.WriteByte('\n')
		return 0
	}
	d, err := s.View()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	if count {
		n, err := d.Count(jseek.ParseDotPath(path)...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		fmt.Fprintln(w, n)
		return 0
	}
	results, err := jseek.QueryTyped(data, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	for _, res := range results {
		writeTyped(w, res, raw)
	}
	return 0
}

// runSealNDJSON: jseek -seal-n in.ndjson out.njstore
func runSealNDJSON(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "jseek: -seal-n needs <input.ndjson> <output.njstore>")
		return 2
	}
	in, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	defer in.Close()
	out, err := os.Create(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	defer out.Close()
	bw := bufio.NewWriter(out)
	n, err := jseek.SealNDJSON(bw, in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	if err := bw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	st, _ := out.Stat()
	fmt.Fprintf(os.Stderr, "sealed %d records -> %s (%d bytes)\n", n, args[1], st.Size())
	return 0
}

// runOpenNDJSON: jseek -open-n store.njstore 'path' [-at N]
func runOpenNDJSON(args []string, raw, count bool, at int) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "jseek: -open-n needs <store.njstore> [path]")
		return 2
	}
	// Extract a trailing "-at N" even when it follows positional args (Go's
	// flag package stops parsing at the first non-flag argument).
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-at" && i+1 < len(args) {
			if n, e := strconv.Atoi(args[i+1]); e == nil {
				at = n
			}
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	args = rest
	s, err := jseek.OpenNDJSONStore(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	defer s.Close()
	path := ""
	if len(args) > 1 {
		path = args[1]
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	emit := func(d *jseek.Document, rawRec []byte) error {
		if path == "" || path == "." {
			out, err := jseek.Compact(rawRec)
			if err != nil {
				return err
			}
			w.Write(out)
			w.WriteByte('\n')
			return nil
		}
		if count {
			n, err := d.Count(jseek.ParseDotPath(path)...)
			if err != nil {
				return nil
			}
			fmt.Fprintln(w, n)
			return nil
		}
		results, err := jseek.QueryTyped(rawRec, path)
		if err != nil {
			return err
		}
		for _, res := range results {
			writeTyped(w, res, raw)
		}
		return nil
	}

	if at >= 0 {
		d, err := s.Record(at)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		rawRec, _ := s.Raw(at)
		if err := emit(d, rawRec); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		return 0
	}
	var d jseek.Document
	for i := 0; i < s.Len(); i++ {
		if _, err := s.RecordInto(&d, i); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		rawRec, _ := s.Raw(i)
		if err := emit(&d, rawRec); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
	}
	return 0
}

// runQueryNDJSON: jseek -q store.njstore [-where cond] [-select f1,f2]
//
//	[-agg field] [-group key] [-limit N]
//
// Runs filter/projection/aggregation on the zero-parse NDJSON substrate.
func runQueryNDJSON(args []string, where, sel, agg, group string, limit int, raw bool) int {
	// Go's flag package stops at the first positional arg, so flags placed after
	// the store path are extracted manually here (same approach as -at).
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch a {
		case "-where":
			where = take()
		case "-select":
			sel = take()
		case "-agg":
			agg = take()
		case "-group":
			group = take()
		case "-limit":
			if n, e := strconv.Atoi(take()); e == nil {
				limit = n
			}
		case "-r":
			raw = true
		default:
			rest = append(rest, a)
		}
	}
	args = rest
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "jseek: -q needs <store.njstore>")
		return 2
	}
	s, err := jseek.OpenNDJSONStore(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	defer s.Close()

	w, err := jseek.ParseWhere(where)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek: bad -where:", err)
		return 2
	}
	var fields []string
	if sel != "" {
		for _, f := range strings.Split(sel, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				fields = append(fields, f)
			}
		}
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	// Aggregation mode.
	if agg != "" {
		if group != "" {
			g, err := s.GroupAggregate(w, group, agg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "jseek:", err)
				return 1
			}
			keys := make([]string, 0, len(g))
			for k := range g {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				a := g[k]
				fmt.Fprintf(out, "%s\tcount=%d\tn=%d\tsum=%s\tavg=%s\tmin=%s\tmax=%s\n",
					k, a.Count, a.N, fmtNum(a.Sum), fmtNum(a.Avg()), fmtNum(a.Min), fmtNum(a.Max))
			}
			return 0
		}
		a, err := s.Aggregate(w, agg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		fmt.Fprintf(out, "count=%d\tn=%d\tsum=%s\tavg=%s\tmin=%s\tmax=%s\n",
			a.Count, a.N, fmtNum(a.Sum), fmtNum(a.Avg()), fmtNum(a.Min), fmtNum(a.Max))
		return 0
	}

	// Count-only mode: -q with -where but no -select/-agg and a -C-like intent is
	// expressed via -select "" plus -limit; here plain -q emits records.
	data, err := s.SelectJSON(w, fields, limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	if raw {
		// Raw mode strips quotes from single-string projections line by line.
		for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
			if len(line) >= 2 && line[0] == '"' && line[len(line)-1] == '"' {
				out.Write(line[1 : len(line)-1])
			} else {
				out.Write(line)
			}
			out.WriteByte('\n')
		}
		return 0
	}
	out.Write(data)
	return 0
}

func fmtNum(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	if math.IsInf(f, -1) {
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// runAppendNDJSON: jseek -append-n store.njstore new.ndjson
func runAppendNDJSON(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "jseek: -append-n needs <store.njstore> <new.ndjson>")
		return 2
	}
	prior, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	add, err := os.Open(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	defer add.Close()
	tmp := args[0] + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	bw := bufio.NewWriter(out)
	total, err := jseek.AppendNDJSON(bw, prior, add)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	if err := bw.Flush(); err != nil {
		out.Close()
		os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	out.Close()
	if err := os.Rename(tmp, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "appended -> %d total records\n", total)
	return 0
}

// runWAL handles cheap-write modes over a sealed base + WAL log.
//
//	-wset store.jstore 'path' 'jsonValue'
//	-wdel store.jstore 'path'
//	-wget store.jstore 'path'
//	-wcompact store.jstore out.jstore
func runWAL(args []string, walPath string, wset, wdel, wget, wcompact, raw bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "jseek: WAL modes need <store.jstore> ...")
		return 2
	}
	w, err := jseek.OpenWAL(args[0], walPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jseek:", err)
		return 1
	}
	defer w.Close()
	switch {
	case wset:
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "jseek: -wset needs <store> <path> <jsonValue>")
			return 2
		}
		keys := jseek.ParseDotPath(args[1])
		if err := w.Set([]byte(args[2]), keys...); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "set %s (%d pending)\n", args[1], w.Pending())
	case wdel:
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "jseek: -wdel needs <store> <path>")
			return 2
		}
		keys := jseek.ParseDotPath(args[1])
		if err := w.Delete(keys...); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "deleted %s (%d pending)\n", args[1], w.Pending())
	case wget:
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "jseek: -wget needs <store> <path>")
			return 2
		}
		keys := jseek.ParseDotPath(args[1])
		v, vt, err := w.Get(keys...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		bw := bufio.NewWriter(os.Stdout)
		writeTyped(bw, jseek.QueryResult{Raw: v, Type: vt}, raw)
		bw.Flush()
	case wcompact:
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "jseek: -wcompact needs <store> <out.jstore>")
			return 2
		}
		if err := w.Compact(); err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		// After compact the base holds everything; write it out via a fresh
		// seal of the compacted payload.
		d, _, err := w.Get()
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		out, err := os.Create(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 2
		}
		bw := bufio.NewWriter(out)
		if err := jseek.Seal(bw, d); err != nil {
			out.Close()
			fmt.Fprintln(os.Stderr, "jseek:", err)
			return 1
		}
		bw.Flush()
		out.Close()
		fmt.Fprintf(os.Stderr, "compacted -> %s\n", args[1])
	}
	return 0
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
