package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shiaho777/jseek"
	"github.com/tidwall/gjson"
)

// TestQueryMatchesGJSON cross-checks jseek's wildcard/filter query surface
// against gjson on shared syntax. Only the subset both implement identically
// is compared (element wildcards, simple comparisons, existence, counts).
func TestQueryMatchesGJSON(t *testing.T) {
	docs := []string{
		`{"users":[{"name":"Ada","age":36,"active":true},{"name":"Bob","age":24,"active":false},{"name":"Cleo","age":41,"active":true}]}`,
		`{"users":[{"name":"Ada","age":36},{"name":"Bob","age":24}]}`,
		`{"users":[]}`,
		`{"users":[{"name":"Ada"},{"name":"Bob","age":1}]}`,
		`{"a":{"x":1,"y":2},"b":{"x":3}}`,
		`{"items":[{"v":1.5},{"v":2},{"v":-3}]}`,
	}
	paths := []string{
		"users.#.name",
		"users.#.age",
		"users.#(age>30).name",
		"users.#(age==24).name",
		"users.#(active==true).name",
		"users.#(name==\"Bob\").age",
		"users.#(age>30)#",
		"users.#(age>30)#.name",
		"users.#",
		"users.#(age>=36)#",
		"items.#(v>1).v",
		"items.#(v<0).v",
		"items.#(v>1)#.v",
	}
	for _, doc := range docs {
		for _, p := range paths {
			gres := gjson.Get(doc, p)
			jres, jerr := jseek.Query([]byte(doc), p)
			if jerr != nil {
				t.Fatalf("jseek.Query(%q) error: %v", p, jerr)
			}
			var gvals []string
			if gres.IsArray() {
				for _, r := range gres.Array() {
					gvals = append(gvals, r.Raw)
				}
			} else if gres.Exists() {
				gvals = []string{gres.Raw}
			}
			// gjson returns strings with quotes in Raw; jseek returns
			// content without quotes. Re-quote using the document's actual
			// value types (a string "0" must not be mistaken for a number).
			var jvals []string
			for _, v := range jres {
				jvals = append(jvals, normalizeRaw2(doc, p, v))
			}
			if fmt.Sprint(gvals) != fmt.Sprint(jvals) {
				t.Errorf("doc=%s path=%s\n gjson=%v\n jseek=%v", doc, p, gvals, jvals)
			}
		}
	}
}

// normalizeRaw2 re-quotes jseek string results to match gjson's Raw form,
// using the document's actual types: walk the same path with gjson over the
// parsed structure to learn whether each harvested field is a string.
func normalizeRaw2(doc, path string, v []byte) string {
	s := string(v)
	// Determine the field type from the path's last segment by inspecting
	// the first document element that has it. Type comes from the document,
	// never from the value's shape (a string may contain '[' or digits).
	field := path
	if j := lastDot(field); j >= 0 {
		field = field[j+1:]
	}
	if field == "#" || field == "" {
		return s // counts / whole elements: verbatim
	}
	root := gjson.Parse(doc)
	var typ gjson.Type
	for _, arr := range []string{"users", "items"} {
		a := root.Get(arr)
		if !a.IsArray() {
			continue
		}
		for _, el := range a.Array() {
			f := el.Get(field)
			if f.Exists() {
				typ = f.Type
				break
			}
		}
		if typ != 0 {
			break
		}
	}
	if typ == gjson.String {
		return `"` + s + `"`
	}
	return s
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func queryBenchDoc() []byte {
	var b strings.Builder
	b.WriteString(`{"users":[`)
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"name":"user_%d","age":%d,"active":%v}`, i, 20+i%40, i%3 == 0)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func BenchmarkQueryAll_Jseek(b *testing.B) {
	data := queryBenchDoc()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := jseek.Query(data, "users.#.name")
		if err != nil || len(out) != 500 {
			b.Fatal("bad query")
		}
	}
}

func BenchmarkQueryAll_GJSON(b *testing.B) {
	data := queryBenchDoc()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := gjson.GetBytes(data, "users.#.name")
		if len(r.Array()) != 500 {
			b.Fatal("bad query")
		}
	}
}

func BenchmarkQueryFilterAll_Jseek(b *testing.B) {
	data := queryBenchDoc()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := jseek.Query(data, "users.#(age>40)#.name")
		if err != nil || len(out) == 0 {
			b.Fatal("bad query")
		}
	}
}

func BenchmarkQueryFilterAll_GJSON(b *testing.B) {
	data := queryBenchDoc()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := gjson.GetBytes(data, "users.#(age>40)#.name")
		if len(r.Array()) == 0 {
			b.Fatal("bad query")
		}
	}
}

// FuzzQueryMatchesGJSON extends the differential check to generated documents
// with a fixed shape and varied scalar values.
func FuzzQueryMatchesGJSON(f *testing.F) {
	f.Add("Ada", 36, true, "Bob", 24, false)
	f.Add("", 0, false, "x", -5, true)
	f.Fuzz(func(t *testing.T, n1 string, a1 int, b1 bool, n2 string, a2 int, b2 bool) {
		if strings.ContainsAny(n1, `"\`) || strings.ContainsAny(n2, `"\`) {
			return // keep fixtures simple; escaping covered by unit tests
		}
		doc := fmt.Sprintf(`{"users":[{"name":%q,"age":%d,"active":%v},{"name":%q,"age":%d,"active":%v}]}`,
			n1, a1, b1, n2, a2, b2)
		for _, p := range []string{"users.#.name", "users.#(age>0).name", "users.#(active==true)#", "users.#(age<=0)#.name"} {
			gres := gjson.Get(doc, p)
			jres, err := jseek.Query([]byte(doc), p)
			if err != nil {
				t.Fatalf("Query(%q): %v", p, err)
			}
			var gvals []string
			if gres.IsArray() {
				for _, r := range gres.Array() {
					gvals = append(gvals, r.Raw)
				}
			} else if gres.Exists() {
				gvals = []string{gres.Raw}
			}
			var jvals []string
			for _, v := range jres {
				jvals = append(jvals, normalizeRaw2(doc, p, v))
			}
			if fmt.Sprint(gvals) != fmt.Sprint(jvals) {
				t.Fatalf("doc=%s path=%s\n gjson=%v\n jseek=%v", doc, p, gvals, jvals)
			}
		}
	})
}