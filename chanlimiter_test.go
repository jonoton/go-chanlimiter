package chanlimiter

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// cleanableItem is a helper struct for testing the Cleanable interface.
// It uses a channel to signal when its Cleanup method has been called.
type cleanableItem struct {
	id          int
	cleanupChan chan int
}

// Cleanup implements the Cleanable interface.
func (c *cleanableItem) Cleanup() {
	// Send the ID to the channel to signal that this specific item was cleaned up.
	// Use a non-blocking send in case the test isn't listening.
	select {
	case c.cleanupChan <- c.id:
	default:
	}
}

// TestCleanupOnOverwrite verifies that Cleanup is called when an item is overwritten.
func TestCleanupOnOverwrite(t *testing.T) {
	cleanupChan := make(chan int, 2)
	item1 := &cleanableItem{id: 1, cleanupChan: cleanupChan}
	item2 := &cleanableItem{id: 2, cleanupChan: cleanupChan}

	// Use a slow rate to ensure item1 is not sent before item2 arrives.
	limiter := New[*cleanableItem](1)
	defer limiter.Stop()

	limiter.Send(item1)
	// Give the collector a moment to process item1.
	time.Sleep(50 * time.Millisecond)

	limiter.Send(item2) // This should cause item1 to be dropped and cleaned up.

	select {
	case cleanedID := <-cleanupChan:
		if cleanedID != 1 {
			t.Errorf("expected item 1 to be cleaned up, but got item %d", cleanedID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for item 1 to be cleaned up")
	}
}

// TestCleanupOnSendDrop verifies that Cleanup is called when a send is dropped.
func TestCleanupOnSendDrop(t *testing.T) {
	cleanupChan := make(chan int, 2)
	item1 := &cleanableItem{id: 1, cleanupChan: cleanupChan}
	item2 := &cleanableItem{id: 2, cleanupChan: cleanupChan}

	// Use a small buffer to force a drop.
	limiter := New[*cleanableItem](1, WithBufferSize[*cleanableItem](1))
	defer limiter.Stop()

	limiter.Send(item1) // This fills the buffer.
	// Immediately send another item. The collector goroutine is unlikely to have
	// run yet, so the buffer should still be full, forcing this send to be dropped.
	limiter.Send(item2)

	select {
	case cleanedID := <-cleanupChan:
		if cleanedID != 2 {
			t.Errorf("expected dropped item 2 to be cleaned up, but got item %d", cleanedID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for dropped item 2 to be cleaned up")
	}
}

// TestCleanupOnStop verifies that Cleanup is called for a held item on shutdown.
func TestCleanupOnStop(t *testing.T) {
	cleanupChan := make(chan int, 1)
	item1 := &cleanableItem{id: 1, cleanupChan: cleanupChan}

	limiter := New[*cleanableItem](1)
	limiter.Send(item1)
	// Give the collector time to receive the item.
	time.Sleep(50 * time.Millisecond)

	limiter.Stop() // This should trigger the cleanup of the held item1.

	select {
	case cleanedID := <-cleanupChan:
		if cleanedID != 1 {
			t.Errorf("expected item 1 to be cleaned up on Stop, but got item %d", cleanedID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for item 1 to be cleaned up on Stop")
	}
}

// TestNoCleanupForNonCleanable ensures the limiter works with non-cleanable types.
func TestNoCleanupForNonCleanable(t *testing.T) {
	// This test simply verifies that the limiter can be instantiated and used
	// with a type like `int` that does not implement Cleanable, without panicking.
	limiter := New[int](10)
	defer limiter.Stop()

	limiter.Send(1)
	limiter.Send(2) // Overwrite

	// The test passes if it completes without a panic.
}

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
}

// TestWithBufferSizeOption verifies that a custom buffer size is applied correctly.
func TestWithBufferSizeOption(t *testing.T) {
	customSize := 5
	limiter := New[int](10, WithBufferSize[int](customSize))
	defer limiter.Stop()

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
		if cap(limiter.input) != rate {
			t.Errorf("expected default buffer size of %d, but got %d", rate, cap(limiter.input))
		}
	})
}

// TestBufferSizeFunctionality proves the buffer works by allowing non-blocking sends.
func TestBufferSizeFunctionality(t *testing.T) {
	limiter := New[int](1, WithBufferSize[int](2))
	defer limiter.Stop()

	limiter.Send(1)
	limiter.Send(2) // This will overwrite item 1.

	select {
	case item := <-limiter.Output():
		if item != 2 {
			t.Errorf("expected to receive the last item (2), but got %d", item)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for item from limiter output")
	}

	select {
	case item := <-limiter.Output():
		t.Errorf("expected no more items, but received %d", item)
	case <-time.After(2 * time.Second):
	}
}

// TestRateLimiting is a robust test that measures items received in a fixed duration.
func TestRateLimiting(t *testing.T) {
	rate := 10 // 10 dps
	duration := 2 * time.Second
	limiter := New[int](rate)
	defer limiter.Stop()

	go func() {
		for i := 0; ; i++ {
			limiter.Send(i)
			time.Sleep(5 * time.Millisecond)
		}
	}()

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

	expectedCount := rate * int(duration.Seconds())
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
