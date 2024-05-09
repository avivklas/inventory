package inventory

import (
	"maps"
	"slices"
	"sync"
)

// DB maintains items under keys indexed also by tags in order to be able to
// evict all items under a specific tag
type DB interface {
	DBViewer
	DBWriter

	// View provides atomic read-only access to the db for the scope of the
	// provided callback, isolated from other operations on the db.
	View(func(viewer DBViewer) error) error

	// Update provides atomic read-write access to the db for the scope of
	// the provided callback.
	Update(func(writer DBWriter) error) error

	// GetOrFill is a nice utility that wraps get, put if not exist and return
	// the value,
	GetOrFill(key string, fill func() (interface{}, error), tags ...string) (val interface{}, err error)

	// Invalidate deletes all keys related to the provided tags
	Invalidate(tags ...string) (deleted []string)
}

// DBViewer represents isolated read access handle to the db
type DBViewer interface {
	// Get safely retrieves a val identified by the provided key
	Get(key string) (val interface{}, ok bool)

	// Iter retrieves all the keys under a tag and let you access each item in each key
	Iter(tag string, fn func(key string, getVal func() (interface{}, bool)) (proceed bool))
}

// DBWriter represents isolated write access handle to the db
type DBWriter interface {
	DBViewer

	// Put safely sets the provided val under the provided key indexed by the
	// provided tags
	Put(key string, val interface{})

	// Tag simply adds tag on a key
	Tag(key string, tags ...string)

	// Invalidate deletes all keys related to the provided tags
	Invalidate(tags ...string) (deleted []string)
}

func NewDB() DB {
	return &db{
		storage: storage{
			map[string]map[string]struct{}{},
			map[string]map[string]struct{}{},
			map[string]interface{}{},
		},
	}
}

type storage struct {
	tagToKeys map[string]map[string]struct{}
	keyToTags map[string]map[string]struct{}
	items     map[string]interface{}
}

type db struct {
	storage

	t transaction

	muR sync.RWMutex
	muW sync.Mutex
}

func (c *db) Invalidate(tags ...string) (deleted []string) {
	_ = c.Update(func(writer DBWriter) error {
		deleted = writer.Invalidate(tags...)

		return nil
	})

	return
}

func (c *db) Tag(key string, tags ...string) {
	_ = c.Update(func(writer DBWriter) error {
		writer.Tag(key, tags...)

		return nil
	})
}

func (c *db) View(viewFn func(DBViewer) error) (err error) {
	c.muR.RLock()
	err = viewFn(cacheView(c.storage))
	c.muR.RUnlock()

	return
}

func (c *db) Update(updateFn func(DBWriter) error) (err error) {
	c.muW.Lock()
	c.t.origin = &c.storage

	if c.t.additions.items == nil || c.t.additions.keyToTags == nil || c.t.additions.tagToKeys == nil {
		c.t.additions = storage{
			map[string]map[string]struct{}{},
			map[string]map[string]struct{}{},
			map[string]interface{}{},
		}
	}
	if c.t.deletions.items == nil || c.t.deletions.keyToTags == nil || c.t.deletions.tagToKeys == nil {
		c.t.deletions = storage{
			map[string]map[string]struct{}{},
			map[string]map[string]struct{}{},
			map[string]interface{}{},
		}
	}

	err = updateFn(&c.t)
	if err == nil {
		c.muR.Lock()
		for k := range c.t.deletions.items {
			delete(c.storage.items, k)
		}
		for k := range c.t.deletions.keyToTags {
			delete(c.storage.keyToTags, k)
		}
		for k := range c.t.deletions.tagToKeys {
			delete(c.storage.tagToKeys, k)
		}

		for k, v := range c.t.additions.items {
			c.storage.items[k] = v
		}
		for k, v := range c.t.additions.keyToTags {
			c.storage.keyToTags[k] = v
		}
		for k, v := range c.t.additions.tagToKeys {
			c.storage.tagToKeys[k] = v
		}

		c.muR.Unlock()
	}

	clear(c.t.additions.items)
	clear(c.t.additions.keyToTags)
	clear(c.t.additions.tagToKeys)
	clear(c.t.deletions.items)
	clear(c.t.deletions.keyToTags)
	clear(c.t.deletions.tagToKeys)

	c.muW.Unlock()

	return
}

func (c *db) Get(key string) (val interface{}, ok bool) {
	c.muR.RLock()
	val, ok = cacheView(c.storage).Get(key)
	c.muR.RUnlock()

	return
}

func (c *db) Iter(tag string, fn func(key string, val func() (interface{}, bool)) (proceed bool)) {
	c.muR.RLock()
	cacheView(c.storage).Iter(tag, fn)
	c.muR.RUnlock()

	return
}

func (c *db) Put(key string, val interface{}) {
	_ = c.Update(func(writer DBWriter) error {
		writer.Put(key, val)

		return nil
	})

	return
}

func (c *db) GetOrFill(key string, fill func() (interface{}, error), tags ...string) (val interface{}, err error) {
	val, ok := c.Get(key)
	if ok {
		return
	}

	err = c.Update(func(writer DBWriter) error {
		val, ok = writer.Get(key)

		if ok {
			return nil
		}

		val, err = fill()
		if err != nil {
			return err
		}

		writer.Put(key, val)
		writer.Tag(key, tags...)

		return nil
	})

	return
}

type cacheView storage

func (c cacheView) Get(key string) (val interface{}, ok bool) {
	val, ok = c.items[key]

	return
}

func (c cacheView) Iter(tag string, fn func(key string, val func() (interface{}, bool)) bool) {
	keys, ok := c.tagToKeys[tag]
	if !ok {
		return
	}

	for k := range keys {
		if !fn(k, func() (interface{}, bool) {
			return c.Get(k)
		}) {
			break
		}
	}

	return
}

type transaction struct {
	origin    *storage
	additions storage
	deletions storage
}

func (c *transaction) Tag(key string, tags ...string) {
	if _, ok := c.additions.keyToTags[key]; !ok {
		_, ok = c.origin.keyToTags[key]
		if ok {
			c.additions.keyToTags[key] = make(map[string]struct{}, len(c.origin.keyToTags[key]))
			maps.Copy(c.additions.keyToTags[key], c.origin.keyToTags[key])
		} else {
			c.additions.keyToTags[key] = make(map[string]struct{})
		}
	}

	for _, tag := range tags {
		if _, ok := c.additions.tagToKeys[tag]; !ok {
			c.additions.tagToKeys[tag], ok = c.origin.tagToKeys[tag]
			if ok {
				c.additions.tagToKeys[tag] = make(map[string]struct{}, len(c.origin.tagToKeys[tag]))
				maps.Copy(c.additions.tagToKeys[tag], c.origin.tagToKeys[tag])
			} else {
				c.additions.tagToKeys[tag] = make(map[string]struct{})
			}
		}

		c.additions.tagToKeys[tag][key] = struct{}{}

		c.additions.keyToTags[key][tag] = struct{}{}
	}
}

func (c *transaction) Get(key string) (val interface{}, ok bool) {
	val, ok = c.additions.items[key]
	if ok {
		return
	}

	originVal, ok := c.origin.items[key]
	if !ok {
		return
	}

	if _, ok = c.deletions.items[key]; !ok {
		return originVal, true
	}

	return
}

func (c *transaction) Iter(tag string, fn func(key string, val func() (interface{}, bool)) bool) {
	_, deleted := c.deletions.tagToKeys[tag]
	if deleted {
		return
	}

	var keys map[string]struct{}
	maps.Copy(keys, c.origin.tagToKeys[tag])

	addedKeys, ok := c.additions.tagToKeys[tag]
	if ok {
		maps.Copy(keys, addedKeys)
	}

	for k := range c.additions.tagToKeys[tag] {
		if _, deleted = c.deletions.items[k]; ok {
			continue
		}

		proceed := fn(k, func() (interface{}, bool) {
			return c.Get(k)
		})

		if !proceed {
			break
		}
	}

	return
}

func (c *transaction) Put(key string, val interface{}) {
	c.additions.items[key] = val

	return
}

func (c *transaction) Invalidate(tags ...string) (deleted []string) {
	for _, tag := range tags {
		keys, ok := c.deletions.tagToKeys[tag]
		if ok {
			continue
		}

		keys, ok = c.additions.tagToKeys[tag]
		if !ok {
			keys, ok = c.origin.tagToKeys[tag]
		}
		if !ok {
			continue
		}

		delete(c.additions.tagToKeys, tag)
		c.deletions.tagToKeys[tag] = nil

		for k := range keys {
			c.deleteKey(k)
			if !slices.Contains(deleted, k) {
				deleted = append(deleted, k)
			}
		}

		c.deleteKey(tag)
		if !slices.Contains(deleted, tag) {
			deleted = append(deleted, tag)
		}
	}

	return
}

func (c *transaction) deleteKey(key string) {
	delete(c.additions.items, key)
	c.deletions.items[key] = nil

	tags, ok := c.additions.keyToTags[key]
	if !ok {
		tags, ok = c.origin.keyToTags[key]
	}

	for tag := range tags {
		if _, ok = c.deletions.tagToKeys[tag]; ok {
			continue
		}

		delete(c.additions.tagToKeys[tag], key)
		c.deletions.tagToKeys[tag] = nil
	}
}
