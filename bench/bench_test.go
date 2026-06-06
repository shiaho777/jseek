package bench

import (
	"strconv"
	"strings"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/tidwall/gjson"
	"github.com/shiahonb777/jseek"
)

// smallFixture is a ~190 byte http-log style record (matches the classic
// jsonparser small-payload benchmark shape).
var smallFixture = []byte(`{"uuid":"de305d54-75b4-431b-adb2-eb6b9e546014","tz":-6,"ua":"Mozilla/5.0 (compatible; MSIE 10.0; Windows NT 6.2; WOW64; Trident/6.0)","st":1234567890,"sid":"de305d54-75b4-431b-adb2-eb6b9e546013","gender":"male"}`)

// makeLarge builds a large JSON document: an object with metadata and a big
// array of user records. We then extract only a few fields, which is exactly
// the case jseek is designed to win.
func makeLarge(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"meta":{"version":"1.4.2","generated":1717200000,"source":"discourse-api"},"page":{"current":1,"total":42},"users":[`)
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
	b.WriteString(`],"trailer":{"checksum":"abc123","ok":true}}`)
	return []byte(b.String())
}

var largeFixture = makeLarge(500)

// ---------- Small payload: read several top-level fields ----------

func BenchmarkSmall_Jseek(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = jseek.GetStringUnsafe(smallFixture, "uuid")
		_, _ = jseek.GetInt(smallFixture, "tz")
		_, _ = jseek.GetStringUnsafe(smallFixture, "ua")
		_, _ = jseek.GetInt(smallFixture, "st")
	}
}

func BenchmarkSmall_JSONParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = jsonparser.Get(smallFixture, "uuid")
		_, _ = jsonparser.GetInt(smallFixture, "tz")
		_, _, _, _ = jsonparser.Get(smallFixture, "ua")
		_, _ = jsonparser.GetInt(smallFixture, "st")
	}
}

// ---------- Large payload: pull a few deep fields ----------
// This is the headline case: the field we want is near the FRONT (meta) and a
// couple are in the array. A lazy extractor should skip the bulk of the data.

func BenchmarkLargeShallow_Jseek(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = jseek.GetStringUnsafe(largeFixture, "meta", "version")
		_, _ = jseek.GetInt(largeFixture, "page", "total")
	}
}

func BenchmarkLargeShallow_JSONParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = jsonparser.Get(largeFixture, "meta", "version")
		_, _ = jsonparser.GetInt(largeFixture, "page", "total")
	}
}

// Pull a field from a specific array element (forces navigation into the array
// but still skips most user records and all unrelated fields).

func BenchmarkLargeIndexed_Jseek(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = jseek.GetStringUnsafe(largeFixture, "users", "[250]", "username")
		_, _ = jseek.GetInt(largeFixture, "users", "[250]", "followers")
	}
}

func BenchmarkLargeIndexed_JSONParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = jsonparser.Get(largeFixture, "users", "[250]", "username")
		_, _ = jsonparser.GetInt(largeFixture, "users", "[250]", "followers")
	}
}

// Iterate the whole array, reading two fields per element.

func BenchmarkLargeArrayEach_Jseek(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = jseek.ArrayEach(largeFixture, func(value []byte, dt jseek.ValueType, off int) bool {
			_, _ = jseek.GetStringUnsafe(value, "username")
			_, _ = jseek.GetInt(value, "followers")
			return true
		}, "users")
	}
}

func BenchmarkLargeArrayEach_JSONParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = jsonparser.ArrayEach(largeFixture, func(value []byte, dt jsonparser.ValueType, off int, err error) {
			_, _, _, _ = jsonparser.Get(value, "username")
			_, _ = jsonparser.GetInt(value, "followers")
		}, "users")
	}
}

// ============================================================================
// Head-to-head vs gjson (the current SOTA lazy extractor)
// ============================================================================

func BenchmarkSmall_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetBytes(smallFixture, "uuid")
		_ = gjson.GetBytes(smallFixture, "tz")
		_ = gjson.GetBytes(smallFixture, "ua")
		_ = gjson.GetBytes(smallFixture, "st")
	}
}

func BenchmarkLargeShallow_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetBytes(largeFixture, "meta.version")
		_ = gjson.GetBytes(largeFixture, "page.total")
	}
}

func BenchmarkLargeIndexed_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetBytes(largeFixture, "users.250.username")
		_ = gjson.GetBytes(largeFixture, "users.250.followers")
	}
}

func BenchmarkLargeArrayEach_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gjson.GetBytes(largeFixture, "users").ForEach(func(_, value gjson.Result) bool {
			_ = value.Get("username")
			_ = value.Get("followers")
			return true
		})
	}
}

// ---------- Multi-path read: jseek.EachKey vs gjson.GetManyBytes vs N×Get ----------
// Reading several fields in ONE pass is the headline feature for "grab a bunch
// of fields from one document".

var multiPathsJseek = [][]string{
	{"meta", "version"},
	{"page", "total"},
	{"users", "[0]", "username"},
	{"users", "[100]", "followers"},
	{"users", "[499]", "name"},
	{"trailer", "ok"},
}

var multiPathsGJSON = []string{
	"meta.version",
	"page.total",
	"users.0.username",
	"users.100.followers",
	"users.499.name",
	"trailer.ok",
}

func BenchmarkMultiPath_JseekEachKey(b *testing.B) {
	b.ReportAllocs()
	// Compile paths once and reuse across documents: the intended hot-loop
	// usage. Querying is then allocation-free.
	compiled := jseek.CompileStrings(multiPathsJseek...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compiled.Each(largeFixture, func(idx int, value []byte, vt jseek.ValueType, err error) {
			_ = value
		})
	}
}

func BenchmarkMultiPath_JseekNGet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range multiPathsJseek {
			_, _ = jseek.GetBytes(largeFixture, p...)
		}
	}
}

func BenchmarkMultiPath_GJSONGetMany(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetManyBytes(largeFixture, multiPathsGJSON...)
	}
}

// ============================================================================
// "Index once, query many": the headline architecture.
// Reading MANY fields from ONE large document. Stateless Get re-scans from the
// top every call; Document indexes once then navigates the compact index.
// ============================================================================

// queryPaths are 12 fields scattered through the large document.
var queryPaths = [][]string{
	{"meta", "version"},
	{"meta", "source"},
	{"page", "total"},
	{"users", "[0]", "username"},
	{"users", "[50]", "followers"},
	{"users", "[100]", "name"},
	{"users", "[250]", "email"},
	{"users", "[400]", "following"},
	{"users", "[499]", "active"},
	{"users", "[300]", "avatar", "url"},
	{"trailer", "checksum"},
	{"trailer", "ok"},
}

var queryPathsDot = []string{
	"meta.version", "meta.source", "page.total",
	"users.0.username", "users.50.followers", "users.100.name",
	"users.250.email", "users.400.following", "users.499.active",
	"users.300.avatar.url", "trailer.checksum", "trailer.ok",
}

// Stateless: every Get re-scans the document from the start.
func BenchmarkManyFields_JseekStatelessGet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range queryPaths {
			_, _ = jseek.GetBytes(largeFixture, p...)
		}
	}
}

// Indexed: Stage-1 once per document, then navigate the index for each field.
func BenchmarkManyFields_JseekIndexed(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := jseek.IndexPooled(largeFixture)
		for _, p := range queryPaths {
			_, _ = d.GetBytes(p...)
		}
		d.Free()
	}
}

// gjson: parsing a Result per field; GetMany still re-scans per call internally.
func BenchmarkManyFields_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetManyBytes(largeFixture, queryPathsDot...)
	}
}

// Amortized extreme: reuse one index across MANY query rounds, the scenario the
// architecture is built for (e.g. a cached document served by many requests).
func BenchmarkManyFields_JseekIndexedReused(b *testing.B) {
	d := jseek.Index(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range queryPaths {
			_, _ = d.GetBytes(p...)
		}
	}
}

// ============================================================================
// A/B: skip-pointer tape vs linear skip, reused index (controlled, same binary)
// ============================================================================

func BenchmarkTapeAB_NoTape(b *testing.B) {
	d := jseek.Index(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range queryPaths {
			_, _ = d.GetBytes(p...)
		}
	}
}

func BenchmarkTapeAB_Tape(b *testing.B) {
	d := jseek.IndexTape(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range queryPaths {
			_, _ = d.GetBytes(p...)
		}
	}
}

// Deep single-element access is where O(1) skip should win most: reach element
// 499 in a 500-element array, repeatedly.
var deepPath = []string{"users", "[499]", "name"}

func BenchmarkTapeAB_DeepNoTape(b *testing.B) {
	d := jseek.Index(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = d.GetBytes(deepPath...)
	}
}

func BenchmarkTapeAB_DeepTape(b *testing.B) {
	d := jseek.IndexTape(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = d.GetBytes(deepPath...)
	}
}

// ============================================================================
// A/B: multi-path EachKey — stateless vs indexed vs indexed+tape (reused index)
// ============================================================================

func BenchmarkEachAB_Stateless(b *testing.B) {
	q := jseek.CompileStrings(multiPathsJseek...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Each(largeFixture, func(idx int, value []byte, vt jseek.ValueType, err error) {})
	}
}

func BenchmarkEachAB_DocNoTape(b *testing.B) {
	q := jseek.CompileStrings(multiPathsJseek...)
	d := jseek.Index(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.EachDoc(d, func(idx int, value []byte, vt jseek.ValueType, err error) {})
	}
}

func BenchmarkEachAB_DocTape(b *testing.B) {
	q := jseek.CompileStrings(multiPathsJseek...)
	d := jseek.IndexTape(largeFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.EachDoc(d, func(idx int, value []byte, vt jseek.ValueType, err error) {})
	}
}
