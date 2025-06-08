/*
Package chanlimiter provides a generic, type-safe mechanism to regulate the flow of
data through a channel at a specified rate.

It is designed for scenarios where data is produced at a high or unpredictable
frequency, but consumption needs to be controlled or "throttled" to a steady pace.
The limiter ensures thread safety and handles backpressure by dropping the oldest
unprocessed data in favor of the most recent item, ensuring the consumer
always processes the most up-to-date information.

Key Features:

  - **Type-Safe with Generics:** Instantiate a Limiter for any Go type (e.g., string,
    int, struct) and work with that type directly, avoiding runtime type assertions.
  - **Rate Limiting:** Control the data flow to a specific number of items per second.
  - **Even Data Dropping:** Prioritizes the most recent data, making it ideal for
    streaming updates where freshness is critical.
  - **Thread-Safe:** Safe for concurrent use by multiple producer goroutines.
  - **Graceful Shutdown:** Provides a Stop() method to safely terminate the background
    goroutine and prevent resource leaks.

Usage:

The following example creates a limiter for `string` types that allows 5 items per
second. A producer sends data at a faster rate (10 items/sec), and the consumer
receives the throttled, type-safe output.

	// Instantiate a limiter for the `string` type at 5 items per second.
	limiter := chanlimiter.New[string](5)
	defer limiter.Stop()

	// Producer sends data faster than the limiter's rate.
	go func() {
		for i := 0; i < 20; i++ {
			limiter.Send(fmt.Sprintf("Event-%d", i))
			time.Sleep(100 * time.Millisecond) // 10 items/sec
		}
	}()

	// Consumer receives data at the regulated rate.
	// The 'msg' variable is of type string, no casting is needed.
	for msg := range limiter.Output() {
		fmt.Printf("Received: %s\n", msg)
	}
*/
package chanlimiter
