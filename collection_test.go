package inventory

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"regexp"
	"testing"
	"time"
)

type meta struct {
	id   string
	name string
}

type fooItem struct {
	meta

	fooValue string
}

type barItem struct {
	meta

	barValue     string
	foos         []*fooItem
	valuePattern string
}

func TestCollection(t *testing.T) {
	db := NewDB()

	foos := []*fooItem{{
		meta:     meta{"1", "foo1"},
		fooValue: "I'm foo",
	}, {
		meta:     meta{"2", "foo2"},
		fooValue: "I'm foo #2",
	}}

	bars := []*barItem{{
		meta:         meta{"1", "bar1"},
		barValue:     "I'm bar",
		foos:         foos,
		valuePattern: `\w+`,
	}}

	barCol := NewCollection[*barItem](db, "bar",
		Extractor(func(load func(in ...*barItem)) { load(bars...) }),
		PrimaryKey("id", func(item *barItem, keyVal func(string)) { keyVal(item.id) }),
		AdditionalKey("name", func(item *barItem, keyVal func(string)) { keyVal(item.name) }),
	)

	fooCol := Infer(barCol, "foo-by-bar", func(src *barItem, f func(kv string, items ...*fooItem)) {
		f(src.id, src.foos...)
	})

	getFooById := fooCol.PrimaryKey("id", func(item *fooItem, keyVal func(string)) { keyVal(item.id) })
	getBarByID := barCol.PrimaryKey("id", func(item *barItem, keyVal func(string)) { keyVal(item.id) })
	barByName := barCol.GetBy("name")
	barsByFooId := barCol.MapBy("foo", func(item *barItem, keyVal func(string)) {
		for _, f := range item.foos {
			keyVal(f.id)
		}
	})

	barCol.Invalidate()

	foo, ok := getFooById("1")
	assert.True(t, ok)
	assert.Equal(t, "I'm foo", foo.fooValue)

	bar, ok := getBarByID("1")
	assert.True(t, ok)
	assert.Equal(t, "I'm bar", bar.barValue)

	bar, ok = barByName("bar1")
	assert.True(t, ok)
	assert.Equal(t, "I'm bar", bar.barValue)

	barList, err := barsByFooId("1")
	if !assert.NoError(t, err) {
		return
	}

	barCol.Invalidate()
	assert.Len(t, barList, 1)

	fooList, err := fooCol.Query("1")
	if !assert.NoError(t, err) {
		return
	}

	assert.Len(t, fooList, 2)

	foos[0].fooValue = "this foo has changed"
	bars[0].barValue = "this bar has changed"

	barCol.Invalidate()

	foo2, ok := getFooById("2")
	assert.True(t, ok)
	assert.Equal(t, "I'm foo #2", foo2.fooValue)

	bar, ok = getBarByID("1")
	assert.True(t, ok)
	assert.Equal(t, "this bar has changed", bar.barValue)

	foo, ok = getFooById("1")
	assert.True(t, ok)
	assert.Equal(t, "this foo has changed", foo.fooValue)

	der := Derive(barCol, "regex-pattern", func(bar *barItem) (out *regexp.Regexp, err error) {
		return regexp.Compile(bar.valuePattern)
	})

	re, err := der(bars[0])
	assert.NoError(t, err)
	assert.True(t, re.MatchString("foo"))

	bars[0].valuePattern = `\d+`

	re, err = der(bars[0])
	assert.NoError(t, err)
	assert.True(t, re.MatchString("foo"))

	barCol.Invalidate()

	re, err = der(bars[0])
	assert.NoError(t, err)
	assert.False(t, re.MatchString("foo"))
}

/*
benchmark result as first committed the solution on a MacBook Pro 2020 model

goos: darwin
goarch: amd64
pkg: github.com/avivklas/inventory
cpu: Intel(R) Core(TM) i7-1068NG7 CPU @ 2.30GHz
Benchmark_collection
Benchmark_collection/get
Benchmark_collection/get-8                             3820045      296.1 ns/op
Benchmark_collection/query_one-to-one_relation
Benchmark_collection/query_one-to-one_relation-8       1074028	    1100 ns/op
Benchmark_collection/query_one-to-many_relation
Benchmark_collection/query_one-to-many_relation-8       797988      1504 ns/op
*/
func Benchmark_collection(b *testing.B) {
	db := NewDB()

	b.SetParallelism(1)

	var foos []*fooItem
	for i := 0; i < 1000; i++ {
		foos = append(foos, &fooItem{
			meta: meta{fmt.Sprintf("%d", i), fmt.Sprintf("foo %d", i)},
		})
	}

	var bars []*barItem
	for i := 0; i < 1000/4; i++ {
		var foos []*fooItem
		for j := 0; j < 4; j++ {
			foos = append(foos, &fooItem{meta: meta{fmt.Sprintf("%d", i*4+j), fmt.Sprintf("bar %d", i*4+j)}})
		}
		bars = append(bars, &barItem{
			meta: meta{fmt.Sprintf("%d", i), fmt.Sprintf("bar %d", i)},
			foos: foos,
		})
	}

	barCol := NewCollection[*barItem](db, "bar",
		Extractor(func(load func(in ...*barItem)) { load(bars...) }),
		PrimaryKey("id", func(item *barItem, keyVal func(string)) { keyVal(item.id) }),
	)

	fooCol := Infer(barCol, "foo-by-bar", func(src *barItem, f func(kv string, items ...*fooItem)) {
		f(src.id, src.foos...)
	})

	barCol.Invalidate()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	b.Run("get", func(b *testing.B) {
		getFoo := fooCol.PrimaryKey("id", func(item *fooItem, keyVal func(string)) { keyVal(item.id) })
		barCol.Invalidate()
		var ok bool
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("%d", rnd.Int63n(1000))
			_, ok = getFoo(key)
			if !assert.True(b, ok, key) {
				return
			}
		}
	})

	b.Run("query one-to-one relation", func(b *testing.B) {
		barsByFooId := barCol.MapBy("foo", func(item *barItem, keyVal func(string)) {
			for _, foo := range item.foos {
				keyVal(foo.id)
			}
		})
		barCol.Invalidate()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			barList, err := barsByFooId(fmt.Sprintf("%d", rnd.Int63n(1000)))
			if !assert.NoError(b, err) {
				b.Fatal(err)
			}

			if !assert.Len(b, barList, 1) {
				return
			}
		}
	})

	b.Run("query one-to-many relation", func(b *testing.B) {
		barCol.Invalidate()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			fooList, err := fooCol.Query(fmt.Sprintf("%d", rnd.Int63n(1000/4)))
			if !assert.NoError(b, err) {
				b.Fatal(err)
			}

			assert.Len(b, fooList, 4)
		}
	})

	b.Run("invalidate", func(b *testing.B) {
		barCol.Invalidate()
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			barCol.Invalidate()
		}
	})
}
