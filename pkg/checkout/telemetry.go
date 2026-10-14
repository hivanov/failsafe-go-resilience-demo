package checkout

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// InMemoryTelemetry provides a comprehensive in-memory, thread-safe implementation of TelemetryRecorder.
// It tracks granular production-grade metrics including:
// - Timeouts: # of requests timed out (per-attempt, overall operation timeout, per-operation breakdown).
// - Retries: # of retries of the same operation by attempt index (1st, 2nd, 3rd), and total retries count.
// - Circuit Breaker: current state (CLOSED, OPEN, HALF_OPEN), state changes over time (transitions),
//   # of requests fast-failed in OPEN state, and rolling error percentages.
// - Bulkheads: # of active requests in processing (saturation), # of waiting requests in queue, # of dropped/rejected requests.
// - Fallbacks: # of requests served by fallback per operation and reason.
type InMemoryTelemetry struct {
	mu sync.Mutex

	// Timeouts
	TimeoutCount         int64
	TimeoutsByOperation  map[string]int64
	AttemptTimeoutsCount int64
	OverallTimeoutsCount int64

	// Retries
	RetryCount                  int64 // Kept for backward compatibility
	TotalRetriesCount           int64
	RetriesByOperation          map[string]int64
	RetriesByOperationAndAttempt map[string]map[int]int64

	// Circuit Breakers
	CBState                     string
	CBStateTransitionsCount     int64
	CBRequestsFastFailedInOpen  map[string]int64
	CBTotalExecutions           map[string]int64
	CBFailedExecutions          map[string]int64

	// Bulkheads & Concurrency Limiters
	BulkheadActiveProcessing map[string]int64
	BulkheadWaitingInQueue   map[string]int64
	BulkheadDroppedRequests  map[string]int64

	// Fallbacks
	FallbackRuns         int64 // Kept for backward compatibility
	FallbackRunsCount    int64
	FallbacksByOperation map[string]int64

	// Chronological Event Stream for inspection and assertions
	Events []string
}

// NewInMemoryTelemetry initializes and returns a new empty InMemoryTelemetry recorder with CLOSED circuit breaker state.
func NewInMemoryTelemetry() *InMemoryTelemetry {
	return &InMemoryTelemetry{
		CBState:                      "CLOSED",
		TimeoutsByOperation:          make(map[string]int64),
		RetriesByOperation:           make(map[string]int64),
		RetriesByOperationAndAttempt: make(map[string]map[int]int64),
		CBRequestsFastFailedInOpen:   make(map[string]int64),
		CBTotalExecutions:            make(map[string]int64),
		CBFailedExecutions:           make(map[string]int64),
		BulkheadActiveProcessing:     make(map[string]int64),
		BulkheadWaitingInQueue:       make(map[string]int64),
		BulkheadDroppedRequests:      make(map[string]int64),
		FallbacksByOperation:         make(map[string]int64),
		Events:                       make([]string, 0),
	}
}

// RecordRetry increments the retry counter and records a retry event by operation and attempt index.
func (t *InMemoryTelemetry) RecordRetry(operation string, attempt int, err error) {
	atomic.AddInt64(&t.TotalRetriesCount, 1)
	atomic.AddInt64(&t.RetryCount, 1)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.RetriesByOperation[operation]++
	if t.RetriesByOperationAndAttempt[operation] == nil {
		t.RetriesByOperationAndAttempt[operation] = make(map[int]int64)
	}
	t.RetriesByOperationAndAttempt[operation][attempt]++

	t.Events = append(t.Events, fmt.Sprintf("[RETRY] op=%s attempt=%d total_op_retries=%d err=%v",
		operation, attempt, t.RetriesByOperation[operation], err))
}

// RecordCircuitBreakerState updates the current circuit breaker state and records a state transition event.
func (t *InMemoryTelemetry) RecordCircuitBreakerState(operation string, state string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.CBState != state {
		t.CBStateTransitionsCount++
	}
	t.CBState = state
	t.Events = append(t.Events, fmt.Sprintf("[CIRCUIT_BREAKER_STATE] op=%s state=%s transitions=%d",
		operation, state, t.CBStateTransitionsCount))
}

// RecordCircuitBreakerFastFail increments the counter of requests intercepted and fast-failed while in OPEN state.
func (t *InMemoryTelemetry) RecordCircuitBreakerFastFail(operation string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CBRequestsFastFailedInOpen[operation]++
	t.Events = append(t.Events, fmt.Sprintf("[CIRCUIT_BREAKER_FAST_FAIL] op=%s total_fast_failed=%d",
		operation, t.CBRequestsFastFailedInOpen[operation]))
}

// RecordCircuitBreakerExecution records an execution outcome to track rolling error percentages.
func (t *InMemoryTelemetry) RecordCircuitBreakerExecution(operation string, failed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CBTotalExecutions[operation]++
	if failed {
		t.CBFailedExecutions[operation]++
	}
}

// GetCircuitBreakerErrorRate returns the error percentage for a given operation.
func (t *InMemoryTelemetry) GetCircuitBreakerErrorRate(operation string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := t.CBTotalExecutions[operation]
	if total == 0 {
		return 0.0
	}
	return (float64(t.CBFailedExecutions[operation]) / float64(total)) * 100.0
}

// RecordTimeout increments the timeout counter and records an operation timeout event with elapsed duration.
func (t *InMemoryTelemetry) RecordTimeout(operation string, elapsedMs int64) {
	atomic.AddInt64(&t.TimeoutCount, 1)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.TimeoutsByOperation[operation]++
	t.Events = append(t.Events, fmt.Sprintf("[TIMEOUT] op=%s elapsed=%dms total_op_timeouts=%d",
		operation, elapsedMs, t.TimeoutsByOperation[operation]))
}

// RecordAttemptTimeout records an individual socket attempt timeout (per-attempt).
func (t *InMemoryTelemetry) RecordAttemptTimeout(operation string, attempt int, elapsedMs int64) {
	atomic.AddInt64(&t.AttemptTimeoutsCount, 1)
	t.RecordTimeout(operation, elapsedMs)
}

// RecordOverallTimeout records an overall operation deadline expiration.
func (t *InMemoryTelemetry) RecordOverallTimeout(operation string, elapsedMs int64) {
	atomic.AddInt64(&t.OverallTimeoutsCount, 1)
	t.RecordTimeout(operation, elapsedMs)
}

// RecordBulkheadState records active processing concurrency, queue depth, and dropped requests.
func (t *InMemoryTelemetry) RecordBulkheadState(bulkhead string, active int64, queued int64, dropped int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.BulkheadActiveProcessing[bulkhead] = active
	t.BulkheadWaitingInQueue[bulkhead] = queued
	t.BulkheadDroppedRequests[bulkhead] = dropped
	t.Events = append(t.Events, fmt.Sprintf("[BULKHEAD] name=%s active=%d queued=%d dropped=%d",
		bulkhead, active, queued, dropped))
}

// RecordFallback increments the fallback counter and records a fallback activation event with the triggering reason.
func (t *InMemoryTelemetry) RecordFallback(operation string, reason string) {
	atomic.AddInt64(&t.FallbackRunsCount, 1)
	atomic.AddInt64(&t.FallbackRuns, 1)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.FallbacksByOperation[operation]++
	t.Events = append(t.Events, fmt.Sprintf("[FALLBACK] op=%s reason=%s total_op_fallbacks=%d",
		operation, reason, t.FallbacksByOperation[operation]))
}

// GetEvents returns a thread-safe copy of all recorded telemetry events in chronological order.
func (t *InMemoryTelemetry) GetEvents() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	copied := make([]string, len(t.Events))
	copy(copied, t.Events)
	return copied
}
