//go:build js && wasm

// Command playground bridges jseek to JavaScript for the zero-install browser
// playground. Build with:
//
//	GOOS=js GOARCH=wasm go build -o playground/jseek.wasm ./playground/wasm
//
// It exposes a single global `jseek` object with query/valid/count/bench
// functions. Everything runs locally in the browser — no data leaves the page.
package main

import (
	"strings"
	"syscall/js"
	"time"

	"github.com/shiaho777/jseek"
)

func main() {
	api := map[string]any{
		"query":   js.FuncOf(query),
		"valid":   js.FuncOf(valid),
		"count":   js.FuncOf(count),
		"bench":   js.FuncOf(bench),
		"version": js.FuncOf(version),
	}
	js.Global().Set("jseek", js.ValueOf(api))
	// Signal readiness to the page (browser only; node has no document).
	doc := js.Global().Get("document")
	if !doc.IsUndefined() && !doc.IsNull() {
		doc.Call("dispatchEvent", js.Global().Get("Event").New("jseek-ready"))
	}
	select {}
}

func version(this js.Value, args []js.Value) any {
	return "jseek WASM (lazy extraction + zero-parse substrate)"
}

// query(data, path) -> {results:[{raw,type}], error}
func query(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errResult("query needs (data, path)")
	}
	data := []byte(args[0].String())
	path := args[1].String()
	res, err := jseek.QueryTyped(data, path)
	if err != nil {
		return errResult(err.Error())
	}
	out := make([]any, len(res))
	for i, r := range res {
		out[i] = map[string]any{
			"raw":  rawJSON(r),
			"type": r.Type.String(),
		}
	}
	return map[string]any{"results": out}
}

// rawJSON renders a typed result as a standalone JSON token (strings quoted).
func rawJSON(r jseek.QueryResult) string {
	if r.Type == jseek.String {
		var b strings.Builder
		b.WriteByte('"')
		for i := 0; i < len(r.Raw); i++ {
			c := r.Raw[i]
			switch c {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if c < 0x20 {
					const hex = "0123456789abcdef"
					b.WriteString(`\u00`)
					b.WriteByte(hex[c>>4])
					b.WriteByte(hex[c&0xf])
				} else {
					b.WriteByte(c)
				}
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	return string(r.Raw)
}

// valid(data) -> {valid, strict}
func valid(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("valid needs (data)")
	}
	data := []byte(args[0].String())
	return map[string]any{
		"valid":  jseek.Valid(data),
		"strict": jseek.ValidStrict(data),
	}
}

// count(data, path) -> {count, error}
func count(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("count needs (data[, path])")
	}
	data := []byte(args[0].String())
	var keys []string
	if len(args) >= 2 && args[1].String() != "" {
		keys = jseek.ParseDotPath(args[1].String())
	}
	n, err := jseek.Count(data, keys...)
	if err != nil {
		return errResult(err.Error())
	}
	return map[string]any{"count": n}
}

// bench(data, path, iters) -> {nsPerOp, results, iters}
// Runs the query iters times and reports average nanoseconds per operation,
// measured with the browser's monotonic clock via time.Now.
func bench(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errResult("bench needs (data, path[, iters])")
	}
	data := []byte(args[0].String())
	path := args[1].String()
	iters := 1000
	if len(args) >= 3 {
		iters = args[2].Int()
		if iters < 1 {
			iters = 1
		}
		if iters > 1000000 {
			iters = 1000000
		}
	}
	// Warm up.
	jseek.QueryTyped(data, path)
	start := time.Now()
	var n int
	for i := 0; i < iters; i++ {
		res, err := jseek.QueryTyped(data, path)
		if err != nil {
			return errResult(err.Error())
		}
		n = len(res)
	}
	elapsed := time.Since(start)
	return map[string]any{
		"nsPerOp": elapsed.Nanoseconds() / int64(iters),
		"results": n,
		"iters":   iters,
	}
}

func errResult(msg string) any {
	return map[string]any{"error": msg}
}
