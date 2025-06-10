package chanlimiter

import (
	"context"
	"sync"
	"time"
)

// Cleanable defines an interface for types that require explicit resource cleanup.
// If a data item passed to the Limiter implements this interface, its Cleanup
// method will be called automatically if the item is dropped.
type Cleanable interface {
	Cleanup()
}

// config holds the configurable parameters for a Limiter.
type config[T any] struct {
	bufferSize int
}

// Option configures a Limiter using the functional options pattern.
type Option[T any] func(*config[T])

// WithBufferSize sets a custom buffer size for the input channel. A larger
// buffer can absorb high-frequency bursts from producers without dropping data,
// giving the internal collector goroutine time to process items.
func WithBufferSize[T any](size int) Option[T] {
	return func(c *config[T]) {
		if size >= 1 {
			c.bufferSize = size
		}
	}
}

// Limiter manages the regulated flow of data of any specific type T.
// It ensures thread-safe data transport from producers to a consumer at a
// fixed rate, dropping the oldest unprocessed data in favor of the most recent.
type Limiter[T any] struct {
	input  chan T
	output chan T
	rate   time.Duration
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// mu protects access to latestData and hasData, which are shared between
	// the collector and ticker goroutines.
	mu         sync.Mutex
	latestData T
	hasData    bool
}

// New creates a new Limiter for any specific data type T with a given rate
// per second. The limiter can be customized with functional options.
func New[T any](rate int, opts ...Option[T]) *Limiter[T] {
	if rate <= 0 {
		rate = 1
	}

	// 1. Establish a smart default for the input buffer size.
	// The default is based on the rate, which helps absorb at least one second
	// of burst traffic. Reasonable bounds are applied to prevent excessive memory usage.
	defaultBufferSize := rate
	if defaultBufferSize < 16 {
		defaultBufferSize = 16
	}
	if defaultBufferSize > 1024 {
		defaultBufferSize = 1024
	}
	cfg := &config[T]{
		bufferSize: defaultBufferSize,
	}

	// 2. Apply any user-provided options to override the defaults.
	for _, opt := range opts {
		opt(cfg)
	}

	// 3. Create and initialize the Limiter instance.
	ctx, cancel := context.WithCancel(context.Background())
	l := &Limiter[T]{
		input:  make(chan T, cfg.bufferSize),
		output: make(chan T, 1),
		rate:   time.Second / time.Duration(rate),
		cancel: cancel,
	}

	l.wg.Add(1)
	go l.process(ctx)
	return l
}

// process is the heart of the limiter. It orchestrates the concurrent operations
// by launching and managing two dedicated goroutines. This pattern decouples data
// collection from rate-limited sending, preventing goroutine starvation where a
// high-frequency producer could otherwise block a low-frequency timer.
func (l *Limiter[T]) process(ctx context.Context) {
	defer l.wg.Done()
	var processWg sync.WaitGroup

	// --- Goroutine 1: The Collector ---
	// Its sole responsibility is to receive data from the input channel as quickly
	// as possible and update the single `latestData` slot. This ensures the
	// limiter is always working with the most up-to-date information.
	processWg.Add(1)
	go func() {
		defer processWg.Done()
		// Using for...range is the idiomatic, race-free way to drain a
		// channel until it is closed. This goroutine exits only when the
		// input channel is closed and has been fully drained.
		for data := range l.input {
			l.mu.Lock()
			if l.hasData {
				if cleanable, ok := any(l.latestData).(Cleanable); ok {
					cleanable.Cleanup()
				}
			}
			l.latestData = data
			l.hasData = true
			l.mu.Unlock()
		}
	}()

	// --- Goroutine 2: The Ticker/Sender ---
	// Its sole responsibility is to tick at the specified rate. On each tick,
	// it sends the currently held `latestData` to the output channel. It does
	// not interact with the input channel at all.
	processWg.Add(1)
	go func() {
		defer processWg.Done()
		ticker := time.NewTicker(l.rate)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				l.mu.Lock()
				if l.hasData {
					// Use a non-blocking send. If the consumer is not ready,
					// we do not want to block the ticker.
					select {
					case l.output <- l.latestData:
						// Sent successfully, so we clear the slot.
						l.hasData = false
					default:
						// Consumer was not ready. We keep `hasData` as true,
						// effectively retrying the send on the next tick
						// unless the collector overwrites the data first.
					}
				}
				l.mu.Unlock()
			case <-ctx.Done():
				// The context was canceled, time to exit.
				return
			}
		}
	}()

	// --- Shutdown Sequence ---
	// This block waits until Stop() is called, then orchestrates a graceful shutdown.
	<-ctx.Done()

	// 1. Close the input channel. This is the signal for the Collector's
	//    for...range loop to drain any remaining buffered items and then terminate.
	close(l.input)

	// 2. Wait for both the Collector and Ticker goroutines to finish their work.
	processWg.Wait()

	// 3. Perform a final cleanup for the very last item that may have been
	//    held by the collector when the system shut down.
	l.mu.Lock()
	if l.hasData {
		if cleanable, ok := any(l.latestData).(Cleanable); ok {
			cleanable.Cleanup()
		}
	}
	l.mu.Unlock()

	// 4. Finally, close the output channel to signal to any consumers that no
	//    more data will be sent.
	close(l.output)
}

// Send submits data to the limiter. This is a non-blocking call.
func (l *Limiter[T]) Send(data T) {
	// A panic can occur if Send is called on a stopped limiter after the
	// input channel has been closed. This recover prevents the application
	// from crashing in this specific race condition.
	defer func() {
		recover()
	}()

	select {
	case l.input <- data:
		// Data was successfully placed in the input buffer.
	default:
		// The input buffer is full, so the data is dropped.
		// If the data is Cleanable, its Cleanup method is called.
		if cleanable, ok := any(data).(Cleanable); ok {
			cleanable.Cleanup()
		}
	}
}

// Output returns the read-only channel for consuming regulated data.
func (l *Limiter[T]) Output() <-chan T {
	return l.output
}

// Stop gracefully shuts down the limiter. This call will block until all
// internal goroutines have finished and all necessary cleanup has been performed.
func (l *Limiter[T]) Stop() {
	l.cancel()
	l.wg.Wait()
}
