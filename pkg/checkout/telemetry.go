package checkout

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// InMemoryTelemetry provides an in-memory, thread-safe implementation of TelemetryRecorder.
// It tracks counts of retries, timeouts, fallbacks, and circuit breaker state transitions,
// and retains an event stream for production inspection and automated test assertions.
type InMemoryTelemetry struct {
	mu           sync.Mutex
	RetryCount   int64
	TimeoutCount int64
	FallbackRuns int64
	CBState      string
	Events       []string
}

// NewInMemoryTelemetry initializes and returns a new empty InMemoryTelemetry recorder with CLOSED circuit breaker state.
func NewInMemoryTelemetry() *InMemoryTelemetry {
	return &InMemoryTelemetry{
		CBState: "CLOSED",
		Events:  make([]string, 0),
	}
}

// RecordRetry increments the retry counter and records a retry event in the event stream.
func (t *InMemoryTelemetry) RecordRetry(operation string, attempt int, err error) {
	atomic.AddInt64(&t.RetryCount, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[RETRY] op=%s attempt=%d err=%v", operation, attempt, err))
}

// RecordCircuitBreakerState updates the current circuit breaker state and records a state transition event.
func (t *InMemoryTelemetry) RecordCircuitBreakerState(operation string, state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CBState = state
	t.Events = append(t.Events, fmt.Sprintf("[CIRCUIT_BREAKER] op=%s state=%s", operation, state))
}

// RecordTimeout increments the timeout counter and records an operation timeout event with elapsed duration.
func (t *InMemoryTelemetry) RecordTimeout(operation string, elapsedMs int64) {
	atomic.AddInt64(&t.TimeoutCount, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[TIMEOUT] op=%s elapsed=%dms", operation, elapsedMs))
}

// RecordFallback increments the fallback counter and records a fallback activation event with the triggering reason.
func (t *InMemoryTelemetry) RecordFallback(operation string, reason string) {
	atomic.AddInt64(&t.FallbackRuns, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[FALLBACK] op=%s reason=%s", operation, reason))
}

// GetEvents returns a thread-safe copy of all recorded telemetry events in chronological order.
func (t *InMemoryTelemetry) GetEvents() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	copied := make([]string, len(t.Events))
	copy(copied, t.Events)
	return copied
}
