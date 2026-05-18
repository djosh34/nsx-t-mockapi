// Package clock owns time access boundaries for deterministic application tests.
package clock

import "time"

// Clock provides the current wall time.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the process wall clock.
type SystemClock struct{}

// Now returns the current wall time.
func (SystemClock) Now() time.Time {
	return time.Now()
}
