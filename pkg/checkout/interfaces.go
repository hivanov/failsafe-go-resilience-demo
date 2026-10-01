// Package checkout provides domain models, decoupled interfaces, fault-tolerance policies,
// and orchestrators for executing resilient, SLA-governed checkout transactions in Go.
//
// Context & Cancellation Best Practice:
//
// In a resilient distributed Go service, all outbound operations and activity interfaces
// MUST accept context.Context as their first parameter and strictly honor cancellation signals.
//
// 1. Context Propagation: Callers must propagate failsafe's exec.Context() down the entire
// call stack to all database drivers, HTTP clients, gRPC stubs, and message queues.
//
// 2. Immediate Resource Release: Downstream implementations must monitor ctx.Done() or use
// context-aware I/O (such as http.NewRequestWithContext) to ensure that when a failsafe-go
// Operation Timeout or Per-Attempt Timeout fires, active TCP sockets, goroutines, and memory buffers
// are terminated immediately rather than leaking in the background.
//
// 3. Early Exit Checks: Orchestrator pipelines should check ctx.Err() before initiating new
// steps to avoid performing wasted work on already-canceled requests.
package checkout

import (
	"context"
)

// PaymentGateway defines the contract for processing 3rd-party credit card, debit,
// and digital wallet transactions over external network boundaries.
//
// Architecture Rule: Implementations MUST accept context.Context and pass it directly
// to downstream network clients (e.g. http.NewRequestWithContext). When a failsafe timeout
// expires or the client disconnects, the context cancellation MUST immediately close the underlying
// TCP connection to prevent socket and goroutine leaks.
type PaymentGateway interface {
	// Charge attempts to authorize and settle a payment for the given request.
	// Returns a PaymentResponse on success or an error (e.g. ErrTransientNetwork, ErrGatewayUnavailable).
	// If ctx is canceled or times out, Charge must abort immediately and return ctx.Err().
	Charge(ctx context.Context, req PaymentRequest) (PaymentResponse, error)
}

// InventoryService defines the contract for reserving and releasing stock in warehouse databases.
//
// Architecture Rule: All lock acquisition and release methods must accept context.Context
// to ensure slow database lock acquisition attempts are canceled promptly when operation budgets expire.
type InventoryService interface {
	// LockInventory acquires an optimistic or pessimistic lock and decrements available stock for the item.
	// Returns ErrInventoryDepleted if insufficient units are available, or ctx.Err() on timeout/cancellation.
	LockInventory(ctx context.Context, itemID string, quantity int) error

	// ReleaseInventory releases a previous stock lock and increments available stock upon transaction failure.
	ReleaseInventory(ctx context.Context, itemID string, quantity int) error
}

// FraudService defines the contract for assessing transaction risk via ML scoring models or heuristic fallback rules.
//
// Architecture Rule: The EvaluateRisk implementation must respect ctx.Done() so that slow ML model
// inference requests are canceled the moment the 100ms operation timeout expires, allowing the orchestrator's
// fallback policy to take effect instantly without orphaned background CPU load.
type FraudService interface {
	// EvaluateRisk analyzes the order payload and returns a calculated RiskScore.
	// If the implementation times out or fails, the caller's policy fallback takes effect.
	EvaluateRisk(ctx context.Context, req OrderRequest) (RiskScore, error)
}

// LoyaltyService defines the contract for asynchronous non-blocking reward point accrual.
//
// As a non-critical soft dependency, operations on this interface should be executed
// asynchronously without blocking the primary critical transaction path, while still bounded
// by an explicit background context with an operation timeout.
type LoyaltyService interface {
	// AccruePoints grants reward credits to the customer account based on the settled amount.
	AccruePoints(ctx context.Context, customerID string, amount float64) error
}

// TelemetryRecorder captures and exports resilience policy lifecycle events for
// Prometheus metrics collection, OpenTelemetry distributed tracing, and real-time alerting.
type TelemetryRecorder interface {
	// RecordRetry is invoked whenever an execution attempt fails and a subsequent retry is scheduled.
	RecordRetry(operation string, attempt int, err error)

	// RecordCircuitBreakerState is invoked when a circuit breaker transitions between CLOSED, OPEN, and HALF_OPEN states.
	RecordCircuitBreakerState(operation string, state string)

	// RecordTimeout is invoked when an operation or attempt exceeds its allotted time budget.
	RecordTimeout(operation string, elapsedMs int64)

	// RecordFallback is invoked when all primary execution attempts fail and a fallback result is supplied.
	RecordFallback(operation string, reason string)
}
