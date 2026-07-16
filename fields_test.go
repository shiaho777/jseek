package jseek

import (
	"strconv"
	"testing"
)

func TestGetFields(t *testing.T) {
	data := makeLargeUsers(50)
	res, err := GetFields(data, []string{"users", "[10]"}, "username", "followers", "id")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len %d", len(res))
	}
	if string(res[0].Raw) != "user_10" || res[0].Type != String {
		t.Fatalf("username %q %v", res[0].Raw, res[0].Type)
	}
	if string(res[1].Raw) != "70" || res[1].Type != Number {
		t.Fatalf("followers %q", res[1].Raw)
	}
	if string(res[2].Raw) != "10" {
		t.Fatalf("id %q", res[2].Raw)
	}
}

func TestEachFieldIntoMatchesGet(t *testing.T) {
	data := makeLargeUsers(30)
	keys := []string{"username", "followers", "active", "missing"}
	var offs [8]int
	got := make(map[int]string)
	EachFieldInto(data, []string{"users", "[7]"}, keys, offs[:], func(idx int, value []byte, vt ValueType, err error) {
		if err != nil {
			got[idx] = "ERR"
			return
		}
		got[idx] = string(value)
	})
	u, _, _, _ := Get(data, "users", "[7]", "username")
	f, _, _, _ := Get(data, "users", "[7]", "followers")
	a, _, _, _ := Get(data, "users", "[7]", "active")
	if got[0] != string(u) || got[1] != string(f) || got[2] != string(a) {
		t.Fatalf("got %#v want %s %s %s", got, u, f, a)
	}
	if got[3] != "ERR" {
		t.Fatalf("missing: %v", got[3])
	}
}

func TestDirectJumpEqualSize(t *testing.T) {
	var b []byte
	b = append(b, '[')
	for i := 0; i < 80; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		id := strconv.Itoa(i)
		for len(id) < 2 {
			id = "0" + id
		}
		b = append(b, `{"id":"`...)
		b = append(b, id...)
		b = append(b, `","v":1}`...)
	}
	b = append(b, ']')
	for _, n := range []int{0, 1, 5, 10, 40, 79} {
		v, _, _, err := Get(b, "["+strconv.Itoa(n)+"]", "id")
		if err != nil {
			t.Fatalf("n=%d %v", n, err)
		}
		want := strconv.Itoa(n)
		for len(want) < 2 {
			want = "0" + want
		}
		if string(v) != want {
			t.Fatalf("n=%d got %s want %s", n, v, want)
		}
	}
}
