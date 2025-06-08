package chanlimiter

import (
	"context"
	"sync"
	"time"
)

// config holds the configurable parameters for a Limiter.
type config[T any] struct {
	bufferSize int
}

// Option configures a Limiter using the functional options pattern.
type Option[T any] func(*config[T])

// WithBufferSize sets a custom buffer size for the input channel.
// A larger buffer acts as a "shock absorber" for high-frequency bursts from
// producers, preventing data from being dropped at the entrance while the
// internal collector goroutine is momentarily busy.
func WithBufferSize[T any](size int) Option[T] {
	return func(c *config[T]) {
		if size >= 1 {
			c.bufferSize = size
		}
	}
}

// Limiter manages the regulated flow of data of a specific type T.
// It ensures thread safety and drops the oldest data in favor of the most recent
// item when the production rate exceeds the consumption rate.
type Limiter[T any] struct {
	input  chan T
	output chan T
	rate   time.Duration
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// mu protects access to the latestData and hasData fields, which are
	// shared between the collector and ticker goroutines.
	mu         sync.Mutex
	latestData T
	hasData    bool
}

// New creates a new Limiter for a specific data type T with a given rate per second.
// It can be configured with functional options like WithBufferSize.
func New[T any](rate int, opts ...Option[T]) *Limiter[T] {
	if rate <= 0 {
		rate = 1
	}

	// 1. Setup default configuration.
	// The default buffer size is based on the rate, which helps absorb
	// at least one second of burst traffic. We apply reasonable bounds.
	defaultBufferSize := rate
	if defaultBufferSize < 16 {
		defaultBufferSize = 16 // A reasonable minimum for low-rate limiters.
	}
	if defaultBufferSize > 1024 {
		defaultBufferSize = 1024 // A reasonable maximum to prevent excessive memory usage.
	}
	cfg := &config[T]{
		bufferSize: defaultBufferSize,
	}

	// 2. Apply any user-provided options to override the defaults.
	for _, opt := range opts {
		opt(cfg)
	}

	// 3. Create the limiter with the final configuration.
	ctx, cancel := context.WithCancel(context.Background())
	l := &Limiter[T]{
		input:  make(chan T, cfg.bufferSize), // Use the final buffer size.
		output: make(chan T, 1),
		rate:   time.Second / time.Duration(rate),
		cancel: cancel,
	}

	l.wg.Add(1)
	go l.process(ctx)
	return l
}

// process is the heart of the limiter. It launches and manages two dedicated
// goroutines to decouple data collection from rate-limited sending. This
// two-goroutine pattern prevents "goroutine starvation," where a high-frequency
// input could prevent a timer from ever being processed in a single select loop.
func (l *Limiter[T]) process(ctx context.Context) {
	defer l.wg.Done()
	var processWg sync.WaitGroup

	// Goroutine 1: The Collector.
	// Its only job is to receive from the input channel as fast as possible
	// and store the most recent value. This ensures the "even dropping"
	// logic is effective, as we're always working with the freshest data.
	processWg.Add(1)
	go func() {
		defer processWg.Done()
		for {
			select {
			case data, ok := <-l.input:
				if !ok {
					// Input channel was closed, time to exit.
					return
				}
				l.mu.Lock()
				l.latestData = data
				l.hasData = true
				l.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Goroutine 2: The Ticker/Sender.
	// Its only job is to tick at a fixed rate. On each tick, it sends
	// whatever data the Collector has stored. It does not listen on the
	// input channel at all.
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
					// Use a non-blocking send to the output. If the consumer
					// isn't ready, we don't want to block the ticker.
					select {
					case l.output <- l.latestData:
						// Sent successfully, so we can forget the data.
						l.hasData = false
					default:
						// Consumer was not ready. We DO NOT change hasData.
						// This means we will retry sending the same item on
						// the next tick, unless the collector overwrites it
						// with newer data first.
					}
				}
				l.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for the context to be canceled (via Stop()), then begin shutdown.
	<-ctx.Done()

	// Close the input channel. This will cause the collector goroutine to
	// exit its loop and terminate.
	close(l.input)

	// Wait for both the collector and ticker goroutines to finish.
	processWg.Wait()

	// Finally, close the output channel. This signals to any consumers
	// that no more data will be sent.
	close(l.output)
}

// Send sends data to the limiter. This is a non-blocking call.
func (l *Limiter[T]) Send(data T) {
	// A panic can occur if Send is called on a stopped limiter after the
	// input channel has been closed. This recover prevents the application
	// from crashing in that edge case.
	defer func() {
		recover()
	}()

	select {
	case l.input <- data:
		// Data successfully sent to the input buffer.
	default:
		// The input buffer is full. This means the producer is sending
		// data much faster than the collector can process it. The data
		// is dropped, which is the desired backpressure behavior.
	}
}

// Output returns the read-only channel for consuming regulated data.
func (l *Limiter[T]) Output() <-chan T {
	return l.output
}

// Stop gracefully shuts down the limiter and all its internal goroutines.
func (l *Limiter[T]) Stop() {
	// Signal the context to be canceled.
	l.cancel()
	// Wait for the main process goroutine to complete its shutdown sequence.
	l.wg.Wait()
}
