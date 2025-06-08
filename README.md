# go-chanlimiter

Package chanlimiter provides a generic, type-safe mechanism to regulate the
flow of data through a channel at a specified rate.

[![Go Reference](https://pkg.go.dev/badge/github.com/jonoton/go-chanlimiter.svg)](https://pkg.go.dev/github.com/jonoton/go-chanlimiter)
[![Go Report Card](https://goreportcard.com/badge/github.com/jonoton/go-chanlimiter?)](https://goreportcard.com/report/github.com/jonoton/go-chanlimiter)

It is designed for scenarios where data is produced at a high or unpredictable
frequency, but consumption needs to be controlled or "throttled" to a steady
pace. The limiter ensures thread safety and handles backpressure by dropping
the oldest unprocessed data in favor of the most recent item.

## Key Features:

  - Type-Safe with Generics: Use with any Go type without runtime type assertions.
  - Thread-Safe: Safe for concurrent use from multiple producer goroutines.
  - Rate Limiting: Control data flow to a specific number of items per second.
  - Even Data Dropping: Prioritizes the most recent data for processing.
  - Optional Cleanup: If a dropped item implements the Cleanable interface,
    its Cleanup() method is called automatically, preventing resource leaks.
  - Configurable Buffering: The input buffer size can be configured to absorb
    bursts of data from high-frequency producers.

## Basic Usage

Create a simple limiter for `int` types at a rate of 5 items per second.

```go
limiter := chanlimiter.New[int](5)
defer limiter.Stop()

// Producer sends data faster than the limiter's rate.
go func() {
	for i := 0; i < 20; i++ {
		limiter.Send(i)
		time.Sleep(100 * time.Millisecond) // 10 items/sec
	}
}()

// Consumer receives data at the regulated rate.
for msg := range limiter.Output() {
	fmt.Printf("Received: %d\n", msg)
}
```

## Advanced Usage: Cleanup Scenarios

The following examples demonstrate the two ways an item's `Cleanup()` method
is called automatically. First, we define a common `Resource` type.

```go
// Define a type that holds a resource and needs cleanup.
type Resource struct {
	ID int
}

// Implement the Cleanable interface.
func (r *Resource) Cleanup() {
	fmt.Printf("Cleaning up resource %d\n", r.ID)
}
```

### Scenario 1: Dropping a New Item (Buffer Full)

This happens when `Send()` is called but the input buffer is full. The new item is
immediately dropped and cleaned up because it was never accepted.

```go
// Create a limiter with a small custom buffer of size 1.
limiter := chanlimiter.New[*Resource](
	1, // 1 item per second
	chanlimiter.WithBufferSize[*Resource](1),
)
defer limiter.Stop()

// Send an item to fill the buffer.
limiter.Send(&Resource{ID: 1})

// Because the collector goroutine is unlikely to have run yet, the buffer
// is still full. This second send is dropped, and its Cleanup()
// method is called automatically.
limiter.Send(&Resource{ID: 2}) // Prints "Cleaning up resource 2"

// The consumer will eventually receive item 1.
for res := range limiter.Output() {
	fmt.Printf("Processing resource %d\n", res.ID) // Prints "Processing resource 1"
}
```

### Scenario 2: Dropping an Old Item (Overwrite)

This happens when a new item arrives and overwrites an older item that the limiter
was holding but had not yet sent. The older item is cleaned up.

```go
limiter := chanlimiter.New[*Resource](1) // 1 item per second
defer limiter.Stop()

// Send an item.
limiter.Send(&Resource{ID: 10})

// Wait long enough for the collector to process the item internally,
// but not long enough for the ticker to send it.
time.Sleep(50 * time.Millisecond)

// This second send will overwrite the held item (ID 10), causing
// its Cleanup() method to be called.
limiter.Send(&Resource{ID: 11}) // Prints "Cleaning up resource 10"

// The consumer will eventually receive the newer item.
for res := range limiter.Output() {
	fmt.Printf("Processing resource %d\n", res.ID) // Prints "Processing resource 11"
}
```
