/*
Package chanlimiter provides a generic, type-safe mechanism to regulate the
flow of data through a channel at a specified rate.

It is designed for scenarios where data is produced at a high or unpredictable
frequency, but consumption needs to be controlled or "throttled" to a steady
pace. The limiter ensures thread safety and handles backpressure by dropping
the oldest unprocessed data in favor of the most recent item.

# Key Features:

  - Type-Safe with Generics: Use with any Go type without runtime type assertions.
  - Thread-Safe: Safe for concurrent use from multiple producer goroutines.
  - Rate Limiting: Control data flow to a specific number of items per second.
  - Even Data Dropping: Prioritizes the most recent data for processing.
  - Optional Cleanup: If a dropped item implements the Cleanable interface,
    its Cleanup() method is called automatically, preventing resource leaks.
  - Configurable Buffering: The input buffer size can be configured to absorb
    bursts of data from high-frequency producers.

# Basic Usage

Create a simple limiter for `int` types at a rate of 5 items per second.

	limiter := chanlimiter.New[int](5)
	defer limiter.Stop()

	/* Producer sends data faster than the limiter's rate. */
	go func() {
		for i := 0; i < 20; i++ {
			limiter.Send(i)
			time.Sleep(100 * time.Millisecond) /* 10 items/sec */
		}
	}()

	/* Consumer receives data at the regulated rate. */
	for msg := range limiter.Output() {
		fmt.Printf("Received: %d\n", msg)
	}

# Advanced Usage with Cleanup

This example shows how to use a custom buffer size and how the automatic
cleanup feature works for types that manage resources.

	/* Define a type that holds a resource and needs cleanup. */
	type Resource struct {
		ID int
	}

	/* Implement the Cleanable interface. */
	func (r *Resource) Cleanup() {
		fmt.Printf("Cleaning up resource %d\n", r.ID)
	}

	/* Create a limiter with a small custom buffer. */
	limiter := chanlimiter.New[*Resource](
		2, /* 2 items per second */
		chanlimiter.WithBufferSize[*Resource](1),
	)
	defer limiter.Stop()

	/* Send two items. The first will be held, the second will fill the buffer. */
	limiter.Send(&Resource{ID: 1})
	limiter.Send(&Resource{ID: 2})

	/* This third item will be dropped because the buffer is full,
	   and its Cleanup() method will be called automatically. */
	limiter.Send(&Resource{ID: 3}) /* Prints "Cleaning up resource 3" */

	/* The consumer will receive the items at the specified rate. */
	for res := range limiter.Output() {
		fmt.Printf("Processing resource %d\n", res.ID)
	}
*/
package chanlimiter
