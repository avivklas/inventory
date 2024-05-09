package inventory

// Getter is a function for fetching 1 item of concrete type by a specific key
type Getter[T any] func(val string) (T, bool)

// Query is a function for fetching list of items of a concrete type by tags
type Query[T any] func(key string, filters ...func(T) bool) ([]T, error)

// Scanner is a function for iterating through items of a concrete type
type Scanner[T any] func(consume func(T) bool, filters ...func(key string) bool)

// Scalar is a function for fetching singular item
type Scalar[T any] func() (T, bool)

// Derivative is a fetch method that is lazily initiated from another item but
// only once
type Derivative[In any, Out any] func(in In) (Out, error)
