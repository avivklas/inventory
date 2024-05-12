# Inventory

Inventory applies IoC for application in-flight data. It shares some 
similarities with DI containers but instead of initializing once - application
data containers are required to define the loading procedure of fresh data from
any source.

It was built while I worked @Cyolo to consolidate caching layer operations where
the cache layer is the only access layer for access. No cold layer. Data is
always prepared on the hot cache. It is rather inefficient in writes (compared to
a kv store), but it's more than ok in reads.

The big advantage of this structure is that if all the data you need in your hot
path fits in your memory, it will spare you from the frustrating mechanisms that
meant for actively reading from the data center or in a centralized storage such
as sql server, mongo db or etcd.

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

### Reload Data
reloading the data is performed as a reaction to invalidation of an item. it
deletes all related items and reloads all the relevant kinds (currently all
data of a kind, not only the invalidated items).
```go
i.Invalidate()
```
the underlying db implements isolated transactions and therefore writes don't
block reads. this means that the data in the db is stale until `Invalidate`
returns.

## Performance
performance is not a key objective of this solution. the idea is to manage fresh
app data in-memory in a way that will be the most comfortable to work with - 
types, indexes, etc... for comparison, it is much faster than in-mem SQLite, but
slower than in-mem kv dbs.  
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
