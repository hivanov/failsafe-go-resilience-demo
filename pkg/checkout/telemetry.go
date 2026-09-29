package checkout

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// InMemoryTelemetry records metrics in memory for verification and test assertions.
type InMemoryTelemetry struct {
	mu           sync.Mutex
	RetryCount   int64
	TimeoutCount int64
	FallbackRuns int64
	CBState      string
	Events       []string
}

func NewInMemoryTelemetry() *InMemoryTelemetry {
	return &InMemoryTelemetry{
		CBState: "CLOSED",
		Events:  make([]string, 0),
	}
}

func (t *InMemoryTelemetry) RecordRetry(operation string, attempt int, err error) {
	atomic.AddInt64(&t.RetryCount, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[RETRY] op=%s attempt=%d err=%v", operation, attempt, err))
}

func (t *InMemoryTelemetry) RecordCircuitBreakerState(operation string, state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CBState = state
	t.Events = append(t.Events, fmt.Sprintf("[CIRCUIT_BREAKER] op=%s state=%s", operation, state))
}

func (t *InMemoryTelemetry) RecordTimeout(operation string, elapsedMs int64) {
	atomic.AddInt64(&t.TimeoutCount, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[TIMEOUT] op=%s elapsed=%dms", operation, elapsedMs))
}

func (t *InMemoryTelemetry) RecordFallback(operation string, reason string) {
	atomic.AddInt64(&t.FallbackRuns, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Events = append(t.Events, fmt.Sprintf("[FALLBACK] op=%s reason=%s", operation, reason))
}

func (t *InMemoryTelemetry) GetEvents() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	copied := make([]string, len(t.Events))
	copy(copied, t.Events)
	return copied
}
