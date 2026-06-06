package jseek_test

import (
	"fmt"
	"strings"

	"github.com/shiahonb777/jseek"
)

func ExampleGet() {
	data := []byte(`{"user":{"name":"Ada","followers":42}}`)
	name, _ := jseek.GetString(data, "user", "name")
	n, _ := jseek.GetInt(data, "user", "followers")
	fmt.Printf("%s has %d followers\n", name, n)
	// Output: Ada has 42 followers
}

func ExampleAt() {
	data := []byte(`{"server":{"port":8080,"tls":true}}`)
	port, _ := jseek.At[int64](data, "server", "port")
	tls := jseek.Or[bool](data, false, "server", "tls")
	fmt.Println(port, tls)
	// Output: 8080 true
}

func ExampleIndex() {
	data := []byte(`{"a":{"b":1},"c":[10,20,30]}`)

	// Build the structural index once, then run many queries against it.
	doc := jseek.Index(data)
	b, _ := doc.GetInt("a", "b")
	c, _ := doc.GetInt("c", "[2]")
	fmt.Println(b, c)
	// Output: 1 30
}

func ExampleGetPath() {
	data := []byte(`{"users":[{"name":"Ada"},{"name":"Bob"}]}`)
	name, _, _, _ := jseek.GetPath(data, "users[1].name")
	fmt.Println(string(name))
	// Output: Bob
}

func ExampleGetPointer() {
	data := []byte(`{"a":{"b":["x","y"]}}`)
	// RFC 6901 JSON Pointer.
	v, _, _, _ := jseek.GetPointer(data, "/a/b/1")
	fmt.Println(string(v))
	// Output: y
}

func ExampleGetMany() {
	data := []byte(`{"name":"Ada","age":36,"admin":true}`)
	res := jseek.GetMany(data,
		[]string{"name"},
		[]string{"age"},
		[]string{"admin"},
	)
	name := res[0].String()
	age, _ := res[1].Int()
	admin, _ := res[2].Bool()
	fmt.Printf("%s %d %t\n", name, age, admin)
	// Output: Ada 36 true
}

func ExampleArrayEach() {
	data := []byte(`{"tags":["go","json","fast"]}`)
	_ = jseek.ArrayEach(data, func(value []byte, dt jseek.ValueType, off int) bool {
		fmt.Println(string(value))
		return true
	}, "tags")
	// Output:
	// go
	// json
	// fast
}

func ExampleSet() {
	data := []byte(`{"user":{"name":"old"}}`)
	out, _ := jseek.Set(data, []byte(`"new"`), "user", "name")
	fmt.Println(string(out))
	// Output: {"user":{"name":"new"}}
}

func ExampleDelete() {
	data := []byte(`{"a":1,"b":2,"c":3}`)
	out := jseek.Delete(data, "b")
	fmt.Println(string(out))
	// Output: {"a":1,"c":3}
}

func ExampleDecoder() {
	// Stream a top-level array (or NDJSON) one element at a time.
	stream := `[{"id":1,"n":"a"},{"id":2,"n":"b"},{"id":3,"n":"c"}]`
	dec := jseek.NewDecoder(strings.NewReader(stream))
	_ = dec.ForEach(func(elem []byte) error {
		id, _ := jseek.GetInt(elem, "id")
		name, _ := jseek.GetString(elem, "n")
		fmt.Printf("%d=%s\n", id, name)
		return nil
	})
	// Output:
	// 1=a
	// 2=b
	// 3=c
}

func ExampleDocument_Pin() {
	config := []byte(`{"service":{"region":"us-east-1"},"limits":{"rps":10000}}`)
	doc := jseek.Index(config)
	q := doc.Pin([]string{"limits", "rps"}, []string{"service", "region"})

	// Repeated reads: each is a verified near-direct address, not a search.
	rps, _, _ := q.Get(0)
	region, _, _ := q.Get(1)
	fmt.Printf("%s %s\n", rps, region)
	// Output: 10000 us-east-1
}

func ExampleTransposeInt() {
	records := [][]byte{
		[]byte(`{"status":200,"latency_ms":12}`),
		[]byte(`{"status":500,"latency_ms":48}`),
		[]byte(`{"status":200,"latency_ms":7}`),
	}
	// One pass extracts the column; aggregate at native-slice speed.
	lat := jseek.TransposeInt(records, 0, "latency_ms")
	var sum int64
	for _, v := range lat {
		sum += v
	}
	fmt.Println(sum)
	// Output: 67
}

func ExampleTranspose() {
	records := [][]byte{
		[]byte(`{"id":1,"region":"us"}`),
		[]byte(`{"id":2,"region":"eu"}`),
	}
	// Multiple columns in one index pass per record.
	f := jseek.Transpose(records, []string{"id"}, []string{"region"})
	ids := f.Int(0, -1)
	regions := f.Strings(1, "")
	fmt.Println(ids, regions)
	// Output: [1 2] [us eu]
}
