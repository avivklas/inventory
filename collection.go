package inventory

import (
	"fmt"
	"slices"
)

// NewCollection creates a Collection of T with the provided opts; PrimaryKey
// and Extractor are mandatory
func NewCollection[T any](db DB, kind string, opts ...CollectionOpt[T]) (c *Collection[T]) {
	c = &Collection[T]{
		db:   db,
		kind: kind,
	}

	return c.With(opts...)
}

// Collection is like a typed access layer to the db defined by a schema but
// because the purpose of this repository is IoC of the data, it also defines
// how the data is loaded from the cold source.
type Collection[T any] struct {
	db         DB
	kind       string
	pk         index[T]
	keys       []index[T]
	extract    extractFn[T]
	inferences []inferFn[T]
}

// With instruments the collection with the provided opts
func (c *Collection[T]) With(opts ...CollectionOpt[T]) *Collection[T] {
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Infer creates a "chained" collection in a way that for every loaded item on
// the base collection, the provided mapFn is called to load the inferred item
func Infer[Base, Inferred any](baseCol *Collection[Base], mapBy string, mapFn mapFn[Base, Inferred]) *InferredCollection[Inferred] {
	inferredCol := NewCollection[Inferred](baseCol.db, mapBy)
	baseCol.inferences = append(baseCol.inferences, func(writer DBWriter, base Base) {
		mapFn(base, func(kv string, items ...Inferred) {
			inferredCol.indexer(items, func(key string, item Inferred) {
				inferredCol.loadItem(writer, key, item)
				baseCol.pk.ref(base, func(v string) {
					tag := mkKey(baseCol.kind, mapBy, kv)
					writer.Tag(key, tag)
				})
			})
		})
	})

	q := func(val string, filters ...func(Inferred) bool) (res []Inferred, err error) {
		inferredCol.db.Iter(mkKey(baseCol.kind, mapBy, val), func(key string, getVal func() (interface{}, bool)) (proceed bool) {
			item, ok := getVal()
			if !ok {
				err = fmt.Errorf("failed to retrieve value of %q", key)
				return false
			}

			t, ok := item.(Inferred)
			if !ok {
				err = fmt.Errorf("expected type %T for %q. got %T", t, key, item)
				return false
			}

			for _, f := range filters {
				if !f(t) {
					return true
				}
			}

			res = append(res, t)

			return true
		})

		return
	}

	return &InferredCollection[Inferred]{inferredCol, q}
}

type InferredCollection[T any] struct {
	*Collection[T]
	q Query[T]
}

// With is just a proxy of the underlying Collection's method
func (i *InferredCollection[T]) With(opts ...CollectionOpt[T]) *InferredCollection[T] {
	i.Collection.With(opts...)

	return i
}

// Query is the Query that created by the inferencee. the key is necessarily the
// primary key of the base collection.
func (i *InferredCollection[T]) Query(key string, filters ...func(T) bool) ([]T, error) {
	return i.q(key, filters...)
}

// Derive creates a Derivative item fetcher that is stored under the
// provided subKey in relation to the provided item
func Derive[In, Out any](collection *Collection[In], name string, fn func(in In) (out Out, err error)) Derivative[In, Out] {
	return func(in In) (out Out, err error) {
		if collection.pk.ref == nil {
			err = fmt.Errorf("collection %q has no primary key", collection.kind)
			return
		}

		var key string
		collection.pk.ref(in, func(v string) {
			key = mkKey(fmt.Sprintf("%s/%s", collection.kind, name), collection.pk.key, v)
		})

		val, err := collection.db.GetOrFill(key, func() (res interface{}, err error) {
			res, err = fn(in)
			if err != nil {
				return
			}

			return
		}, collection.kind)

		if err != nil {
			return
		}

		out, ok := val.(Out)
		if !ok {
			err = fmt.Errorf("type assertion error. expected: %T; actual: %T", out, val)
		}

		return
	}
}

// Extractor sets the extractFn of the collection. extractFn is a function
// that extracts the data from the origin source
func Extractor[T any](x extractFn[T]) CollectionOpt[T] {
	return func(c *Collection[T]) {
		c.extract = x
	}
}

// PrimaryKey sets the primary index of the collection
func PrimaryKey[T any](name string, value indexFn[T]) CollectionOpt[T] {
	return func(c *Collection[T]) {
		c.addIndex(c.kind, name, true, value)
	}
}

// AdditionalKey adds a secondary index of the collection
func AdditionalKey[T any](name string, value indexFn[T]) CollectionOpt[T] {
	return func(c *Collection[T]) {
		c.addIndex(c.kind, name, false, value)
	}
}

func (c *Collection[T]) addIndex(kind, key string, primary bool, value indexFn[T]) bool {
	if slices.ContainsFunc(c.keys, func(i index[T]) bool { return i.key == key }) {
		return false
	}

	idx := index[T]{primary, kind, key, value}
	if primary {
		c.pk = idx
	} else {
		c.keys = append(c.keys, idx)
	}

	return true
}

func (c *Collection[T]) index(key string, primary bool, value indexFn[T]) Getter[T] {
	c.addIndex(c.kind, key, primary, value)

	return c.getter(key, primary)
}

func (c *Collection[T]) getter(key string, primary bool) Getter[T] {
	return func(val string) (t T, ok bool) {
		var i interface{}
		if !primary {
			c.db.Iter(mkKey(c.kind, key, val), func(key string, getVal func() (interface{}, bool)) (proceed bool) {
				i, ok = getVal()
				return false
			})
		} else {
			i, ok = c.db.Get(mkKey(c.kind, key, val))
		}

		if !ok {
			return
		}

		t, ok = i.(T)

		return
	}
}

// PrimaryKey creates a primary index on the collection using an indexFn
//
// for example;
// c.PrimaryKey("id", func(f Foo, val func(string)) { val(f.id) })
func (c *Collection[T]) PrimaryKey(key string, value indexFn[T]) Getter[T] {
	return c.index(key, true, value)
}

// AdditionalKey creates an additional index on the collection using an indexFn
//
// for example;
// c.AdditionalKey("name", func(f Foo, val func(string)) { val(f.name) })
func (c *Collection[T]) AdditionalKey(key string, value indexFn[T]) Getter[T] {
	return c.index(key, false, value)
}

// GetBy creates a getter from existing index
func (c *Collection[T]) GetBy(key string) Getter[T] {
	return c.getter(key, key == c.pk.key)
}

// Scalar creates a "static" Getter that will require no key
func (c *Collection[T]) Scalar(key, value string) Scalar[T] {
	k := mkKey(c.kind, key, value)

	return func() (t T, ok bool) {
		i, ok := c.db.Get(k)
		if !ok {
			return
		}

		t, ok = i.(T)

		return
	}
}

// MapBy creates a Query from the provided key mapped by the provided indexFn,
// to be used for querying the collection by a non-unique attribute
func (c *Collection[T]) MapBy(key string, ref indexFn[T]) Query[T] {
	c.addIndex(c.kind, key, false, ref)
	return func(val string, filters ...func(T) bool) (res []T, err error) {
		c.db.Iter(mkKey(c.kind, key, val), func(key string, getVal func() (interface{}, bool)) (proceed bool) {
			item, ok := getVal()
			if !ok {
				err = fmt.Errorf("failed to retrieve value of %q", key)
				return false
			}

			t, ok := item.(T)
			if !ok {
				err = fmt.Errorf("expected type %T for %q. got %T", t, key, item)
				return false
			}

			for _, f := range filters {
				if !f(t) {
					return true
				}
			}

			res = append(res, t)

			return true
		})

		return
	}
}

// Scan iterates over all items in the collection, not sorted
func (c *Collection[T]) Scan(consume func(T) bool, filters ...func(T) bool) {
	var (
		key  string
		ok   bool
		item interface{}
		t    T
	)

	c.db.Iter(c.kind, func(itemKey string, getVal func() (interface{}, bool)) (proceed bool) {
		_, key, _, ok = parseKey(itemKey)
		if !ok || key != c.pk.key {
			return true
		}

		item, ok = getVal()
		if !ok {
			return true
		}

		t, ok = item.(T)
		if !ok {
			return true
		}

		for _, f := range filters {
			if !f(t) {
				return true
			}
		}

		return consume(t)
	})
}

// Invalidate reloads all data from the origin source, defined by the Extractor
func (c *Collection[T]) Invalidate() {
	_ = c.db.Update(func(writer DBWriter) error {
		writer.Invalidate(c.kind)
		c.Load(writer)

		return nil
	})
}

// Load loads all data from the origin source, defined by the Extractor
func (c *Collection[T]) Load(writer DBWriter) {
	c.extract(func(items ...T) {
		c.indexer(items, func(key string, item T) {
			c.loadItem(writer, key, item)
		})
	})
}

func (c *Collection[T]) loadItem(writer DBWriter, key string, item T) {
	writer.Put(key, item)
	writer.Tag(key, c.kind)

	c.tagItemWithIndexes(writer, key, item)

	for _, infer := range c.inferences {
		infer(writer, item)
	}

	return
}

func (c *Collection[T]) tagItemWithIndexes(writer DBWriter, key string, item T) {
	for _, idx := range c.keys {
		idx.ref(item, func(v string) {
			tag := mkKey(c.kind, idx.key, v)
			writer.Tag(key, tag)
		})
	}
}

func (c *Collection[T]) indexer(items []T, fn func(key string, item T)) {
	if c.pk.ref == nil {
		return
	}

	for _, item := range items {
		c.pk.ref(item, func(v string) {
			fn(mkKey(c.kind, c.pk.key, v), item)
		})
	}
}

type index[T any] struct {
	pk   bool
	kind string
	key  string
	ref  indexFn[T]
}

type CollectionOpt[T any] func(*Collection[T])

type indexFn[T any] func(item T, keyVal func(string))

type mapFn[From, To any] func(From, func(kv string, items ...To))

type inferFn[T any] func(DBWriter, T)

type extractFn[T any] func(load func(in ...T))
