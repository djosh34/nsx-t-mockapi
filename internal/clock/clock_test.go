package clock

import (
	"testing"
	"time"
)

func TestSystemClockReturnsWallTime(t *testing.T) {
	t.Parallel()

	if (SystemClock{}).Now().IsZero() {
		t.Fatal("SystemClock.Now() returned zero time")
	}
}

func TestFakeClockNowAndAdvance(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	fake := NewFakeClock(start)

	if got := fake.Now(); got != start {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	fake.Advance(2 * time.Second)
	if got := fake.Now(); got != start.Add(2*time.Second) {
		t.Fatalf("Now() after Advance = %v, want %v", got, start.Add(2*time.Second))
	}
}
