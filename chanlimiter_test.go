package chanlimiter

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestNewLimiter verifies the correct initialization of a new Limiter.
func TestNewLimiter(t *testing.T) {
	t.Run("PositiveRate", func(t *testing.T) {
		rate := 10
		limiter := New[int](rate)
		defer limiter.Stop()

		if limiter == nil {
			t.Fatal("New() returned nil")
		}
		expectedRateDuration := time.Second / time.Duration(rate)
		if limiter.rate != expectedRateDuration {
			t.Errorf("expected rate duration %v, got %v", expectedRateDuration, limiter.rate)
		}
	})

	t.Run("ZeroRateDefaultsToOne", func(t *testing.T) {
		limiter := New[int](0)
		defer limiter.Stop()

		expectedRateDuration := time.Second / 1
		if limiter.rate != expectedRateDuration {
			t.Errorf("expected rate duration for zero rate to be %v, got %v", expectedRateDuration, limiter.rate)
		}
	})
}

// TestWithBufferSizeOption verifies that a custom buffer size is applied correctly.
func TestWithBufferSizeOption(t *testing.T) {
	customSize := 5
	limiter := New[int](10, WithBufferSize[int](customSize))
	defer limiter.Stop()

	// Verify the capacity of the input channel reflects the option.
	if cap(limiter.input) != customSize {
		t.Errorf("expected buffer size of %d, but got %d", customSize, cap(limiter.input))
	}
}

// TestDefaultBufferSize verifies that a smart default buffer is applied.
func TestDefaultBufferSize(t *testing.T) {
	t.Run("RateWithinBounds", func(t *testing.T) {
		rate := 50
		limiter := New[int](rate) // No option provided
		defer limiter.Stop()
		// Default buffer should equal the rate.
		if cap(limiter.input) != rate {
			t.Errorf("expected default buffer size of %d, but got %d", rate, cap(limiter.input))
		}
	})

	t.Run("RateBelowMinimum", func(t *testing.T) {
		rate := 10
		limiter := New[int](rate)
		defer limiter.Stop()
		// Default should be capped at the minimum of 16.
		if cap(limiter.input) != 16 {
			t.Errorf("expected default buffer to be capped at minimum 16, but got %d", cap(limiter.input))
		}
	})
}

// TestBufferSizeFunctionality proves the buffer works by allowing non-blocking sends.
func TestBufferSizeFunctionality(t *testing.T) {
	// Create a limiter with a very slow rate but a known buffer size.
	limiter := New[int](1, WithBufferSize[int](2))
	defer limiter.Stop()

	// These two sends should succeed immediately without blocking.
	limiter.Send(1)
	limiter.Send(2) // This will overwrite item 1.

	// The limiter's "even dropping" means we should only ever receive the LAST item.
	select {
	case item := <-limiter.Output():
		if item != 2 {
			t.Errorf("expected to receive the last item (2), but got %d", item)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for item from limiter output")
	}

	// Verify that no other items are sent.
	select {
	case item := <-limiter.Output():
		t.Errorf("expected no more items, but received %d", item)
	case <-time.After(2 * time.Second):
		// This is the expected outcome.
	}
}

// TestRateLimiting is a robust test that measures items received in a fixed duration.
func TestRateLimiting(t *testing.T) {
	rate := 10 // 10 dps
	duration := 2 * time.Second
	limiter := New[int](rate)
	defer limiter.Stop()

	// Start a continuous producer to ensure data is always available.
	go func() {
		for i := 0; ; i++ {
			limiter.Send(i)
			time.Sleep(5 * time.Millisecond) // Send faster than the rate limit.
		}
	}()

	// Allow a moment for the system to stabilize before measuring.
	time.Sleep(200 * time.Millisecond)

	receivedCount := 0
	timeout := time.After(duration)

ConsumerLoop:
	for {
		select {
		case <-limiter.Output():
			receivedCount++
		case <-timeout:
			break ConsumerLoop
		}
	}

	expectedCount := rate * int(duration.Seconds()) // 10 * 2 = 20
	// Increase margin to 2 to account for minor scheduler variances in tests.
	margin := 2
	if receivedCount < expectedCount-margin || receivedCount > expectedCount+margin {
		t.Errorf("expected to receive around %d items in %v, but got %d", expectedCount, duration, receivedCount)
	}
}

// TestRateIsCorrect measures the time it takes to receive a set number of items.
func TestRateIsCorrect(t *testing.T) {
	rate := 10 // 10 dps
	itemsToReceive := 20
	limiter := New[int](rate)
	defer limiter.Stop()

	go func() {
		for i := 0; ; i++ {
			limiter.Send(i)
			time.Sleep(1 * time.Millisecond)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	startTime := time.Now()

	for i := 0; i < itemsToReceive; i++ {
		<-limiter.Output()
	}

	elapsedTime := time.Since(startTime)
	expectedDuration := time.Duration(itemsToReceive) * (time.Second / time.Duration(rate))
	margin := expectedDuration / 4
	minDuration := expectedDuration - margin
	maxDuration := expectedDuration + margin

	if elapsedTime < minDuration || elapsedTime > maxDuration {
		t.Errorf("expected to receive %d items in ~%v, but it took %v", itemsToReceive, expectedDuration, elapsedTime)
	}
}

// TestEvenDropping verifies that the limiter prioritizes the most recent data.
func TestEvenDropping(t *testing.T) {
	limiter := New[int](1)
	defer limiter.Stop()

	for i := 0; i < 100; i++ {
		limiter.Send(i)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(1100 * time.Millisecond)

	var receivedData int
	select {
	case receivedData = <-limiter.Output():
	default:
		t.Fatal("did not receive any data after waiting")
	}

	if receivedData < 80 {
		t.Errorf("expected a high-numbered item (the most recent), but got %d", receivedData)
	}
}

// TestStop verifies graceful shutdown of the limiter.
func TestStop(t *testing.T) {
	limiter := New[string](10)
	time.Sleep(50 * time.Millisecond)
	limiter.Stop()

	select {
	case _, ok := <-limiter.Output():
		if ok {
			t.Error("output channel should be closed after Stop(), but it's not")
		}
	case <-time.After(1 * time.Second):
		t.Error("limiter did not close the output channel within 1 second")
	}

	limiter.Send("test")
}

// TestConcurrentProducers ensures the limiter is safe for concurrent writes.
func TestConcurrentProducers(t *testing.T) {
	rate := 20
	limiter := New[string](rate)
	defer limiter.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				limiter.Send(fmt.Sprintf("p%d-msg%d", producerID, j))
				time.Sleep(time.Second / 100)
			}
		}(i)
	}
	wg.Wait()
}
