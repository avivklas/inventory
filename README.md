# Inventory
![Build Status](https://img.shields.io/github/actions/workflow/status/avivklas/inventory/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/avivklas/inventory?style=flat-square)](https://goreportcard.com/report/github.com/avivklas/inventory)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](https://github.com/avivklas/inventory/blob/master/LICENSE)

Inventory applies IoC (Inversion of Control) for application's hot-reloaded data. 
It shares some similarities with DI containers but instead of initializing once,
application data containers are required to define the loading procedure of
fresh data from any cold source. the application then enjoys an always-fresh,
in-mem, indexed data as a dependency that can be passed through structs or
funcs.

It was built while I worked @Cyolo to consolidate caching layer operations where
the cache layer is the only access layer for access. No cold layer. Data is
always prepared on the hot cache. It is rather inefficient in writes (compared to
a kv store), but it's more than ok in reads.

The big advantage of this structure is that if all the data you need in your hot
path fits in your memory, it will spare you from the frustrating mechanisms that
meant for actively reading from the data center or in a centralized storage such
as sql server, mongo db or etcd.

## TL;DR
In the following pattern, that might be familiar, some client holds an API key
might get updated by a different service. A management console, perhaps. In
order to avoid expensive calls to the DB in realtime, the client holds the key
in memory. In this pattern, the service needs to provide a method to update the
key from outside in a thread-safe manner.
```go
type AcmeClient struct {
	apiKey string
	client *http.Client
	mu     sync.RWMutex
}

func (c *AcmeClient) UpdateAPIKey(apiKey string) {
	c.mu.Lock()
	c.apiKey = apiKey
	c.mu.Unlock()
}

func (c *AcmeClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.RLock()
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	c.mu.RUnlock()
	
	return c.client.Do(req)
}
```
Inventory's philosophy is saying that the key should be a dependency and that the
service shouldn't include the logic of managing the lifecycle of this value. 
```go
type AcmeClient struct {
	apiKey inventory.Getter[string]
	client *http.Client
}

func (c *AcmeClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey()))
	
	return c.client.Do(req)
}
```

## Components

### `DB`

a primitive storage layer. it's best if it's shared among collections and this is
why it is initialized independently.

**how to init:**
```go
db := DB()
```

### `Extractor`

a simple func that you implement in order to load a specific kind to the
collection from the "cold" source.  
here's an example of loading `foo` from an SQL db:
```go
Extractor(func(add ...Item) {
    rows, err := db.Query("select id, name from foo")
    if err != nil {
        return
    }
    defer rows.Close()
	
    var foo foo
    for rows.Next() {
        err = rows.Scan(&foo)
        if err != nil {
            return
        }
		
        add(&foo)
    }
})
```

### `Collection`

a high-level, typed, data access layer for mapping and querying the data by
the application needs. 
the required `DB` instance is an interface and you can provide your
implementation if needed. for example, you can provide an implementation that
uses a disk if your dataset is too big.

**how to init:**
```go
books := NewCollection[*book](db, "books", 
	Extractor(func(load func(in ...*book)) {
		rs := someDB.QueryAllBooks()
		for rs.Next() {
			var book *book
			err := rs.scan(book)
			load(book)
		}
		rs.Close()
	}), 
	PrimaryKey("id", func(book *book, val func(string)) { val(book.id) }),
)
```
**how to use**:

creating additional keys for unique properties will provide you with `Getter`
by the provided key:
```go
bookByName := books.AdditionalKey("name", func(book *book, keyVal func(string)) { val(book.name) }),
dune, ok := bookByName("Dune")
```

you can use the `Getter` as a dependency for some struct:
```go
type bookService struct {
	bookByID inventory.Getter[*book]
}

func (bs *bookService) getBook(id string) (*book, bool) {
	return bs.bookByID(id)
}
```

you can also map the items by a key that will yield a list for a given value
of the provided key:
```go
bookByAuthor := books.MapBy("author", func(book *book, val func(string)) { val(book.author) }),
daBooks, err := bookByAuthor("Douglas Adams")
```

or simply iterating over all items in the collection, with the ability to stop
whenever you are done:
```go
books.Scan(func(book *book) bool {
	if whatINeeded(book) {
		// do something with it
		...
		// maybe stop?
		return false
	} 
	
	// proceed to next book
	return true
})
```

another useful gem is called `Derivative` - it is meant for creating objects
based on hot-reloaded data - automatically and only once:

```go
bookTags := inventory.Derive[*book, []string](books, "tags", func(book *book) ([]string, error) {
	text := loadText(book)
	return calculateTags(text)
})
```

so now you can call `bookTags` with a book and always get the tags relevant to
the book at its latest state. this will always be invalidated as well and
re-calculated when required but only once per reload of the original book.


### Reload Data
reloading the data is performed as a reaction to invalidation of a collection. 
it deletes all related items from related collection and reloads all the
relevant kinds (currently all data of a kind, not only the invalidated items).
```go
collection.Invalidate()
```
the underlying db implements isolated transactions and therefore writes don't
block reads. this means that the data in the db is stale until `Invalidate`
returns.

## Performance
performance is not a key objective of this solution. the idea is to manage fresh
app data in-memory in a way that will be the most comfortable to work with - 
types, indexes, etc... for comparison, it is much faster than in-mem SQLite, but
slower than in-mem embedded kv dbs.  
if performance is more important for you than readability
then you should look for other solutions.

benchmark result on a MacBook Pro 2020 model

```shell
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
```
