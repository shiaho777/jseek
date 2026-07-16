package jseek

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestFindIndexStrideHomogeneous(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 100; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user_%d"}`, i, i)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	for _, n := range []int{0, 1, 9, 10, 50, 99} {
		got, ok := findIndex(data, 0, n)
		if !ok {
			t.Fatalf("n=%d not found", n)
		}
		wantPrefix := []byte(`{"id":` + strconv.Itoa(n))
		if got+len(wantPrefix) > len(data) || string(data[got:got+len(wantPrefix)]) != string(wantPrefix) {
			t.Fatalf("n=%d at %d got %q want prefix %q", n, got, data[got:min(got+32, len(data))], wantPrefix)
		}
		v, vt, _, err := Get(data, "["+strconv.Itoa(n)+"]", "name")
		if err != nil || vt != String {
			t.Fatalf("Get n=%d: %v %v", n, err, vt)
		}
		if string(v) != "user_"+strconv.Itoa(n) {
			t.Fatalf("Get name n=%d: %s", n, v)
		}
	}
}

func TestFindIndexStrideNestedObjectArrayNoFalsePositive(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"items":[{"x":1},{"x":2},{"x":3}],"tail":%d}`, i, i*10)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	for _, n := range []int{0, 1, 5, 15, 29} {
		v, _, _, err := Get(data, "["+strconv.Itoa(n)+"]", "id")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if string(v) != strconv.Itoa(n) {
			t.Fatalf("n=%d id=%s", n, v)
		}
		v, _, _, err = Get(data, "["+strconv.Itoa(n)+"]", "tail")
		if err != nil || string(v) != strconv.Itoa(n*10) {
			t.Fatalf("n=%d tail=%s err=%v", n, v, err)
		}
		v, _, _, err = Get(data, "["+strconv.Itoa(n)+"]", "items", "[1]", "x")
		if err != nil || string(v) != "2" {
			t.Fatalf("n=%d items[1].x=%s err=%v", n, v, err)
		}
	}
}

func TestFindIndexStrideDigitGrowth(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 150; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"n":%d}`, i, i*7)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	for _, n := range []int{0, 8, 9, 10, 98, 99, 100, 149} {
		v, _, _, err := Get(data, "["+strconv.Itoa(n)+"]", "id")
		if err != nil || string(v) != strconv.Itoa(n) {
			t.Fatalf("n=%d id=%s err=%v", n, v, err)
		}
	}
}

func TestFindIndexStrideLargeFixture(t *testing.T) {
	data := makeLargeUsers(500)
	for _, n := range []int{0, 1, 9, 10, 99, 100, 250, 499} {
		u, _, _, err := Get(data, "users", "["+strconv.Itoa(n)+"]", "username")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		want := "user_" + strconv.Itoa(n)
		if string(u) != want {
			t.Fatalf("n=%d username=%s want %s", n, u, want)
		}
		f, err := GetInt(data, "users", "["+strconv.Itoa(n)+"]", "followers")
		if err != nil || f != int64(n*7) {
			t.Fatalf("n=%d followers=%d err=%v", n, f, err)
		}
	}
}

func makeLargeUsers(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"meta":{"version":"1.4.2"},"users":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		id := strconv.Itoa(i)
		b.WriteString(`{"id":`)
		b.WriteString(id)
		b.WriteString(`,"username":"user_`)
		b.WriteString(id)
		b.WriteString(`","name":"User Number `)
		b.WriteString(id)
		b.WriteString(`","email":"user`)
		b.WriteString(id)
		b.WriteString(`@example.com","bio":"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore.","followers":`)
		b.WriteString(strconv.Itoa(i * 7))
		b.WriteString(`,"following":`)
		b.WriteString(strconv.Itoa(i * 3))
		b.WriteString(`,"active":true,"avatar":{"url":"https://cdn.example.com/avatars/`)
		b.WriteString(id)
		b.WriteString(`.png","width":460,"height":460},"badges":["member","reader","editor"]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func TestFindIndexMixedTypesFallback(t *testing.T) {
	data := []byte(`[{"a":1},2,"x",{"a":3},true,{"a":4}]`)
	v, _, _, err := Get(data, "[5]", "a")
	if err != nil || string(v) != "4" {
		t.Fatalf("got %s err=%v", v, err)
	}
	v, _, _, err = Get(data, "[2]")
	if err != nil || string(v) != `x` && string(v) != `"x"` {
		if string(v) != "x" {
			t.Fatalf("index 2: %q err=%v", v, err)
		}
	}
}
