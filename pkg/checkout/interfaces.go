package checkout

import (
	"context"
)

// PaymentGateway defines the contract for processing 3rd-party credit card, debit,
// and digital wallet transactions over external network boundaries.
//
// Implementations must honor context cancellation to ensure socket connections
// are closed immediately when failsafe timeouts expire.
type PaymentGateway interface {
	// Charge attempts to authorize and settle a payment for the given request.
	// Returns a PaymentResponse on success or an error (e.g. ErrTransientNetwork, ErrGatewayUnavailable).
	Charge(ctx context.Context, req PaymentRequest) (PaymentResponse, error)
}

// InventoryService defines the contract for reserving and releasing stock in warehouse databases.
//
// Implementations must ensure thread-safety and support transactional rollbacks.
type InventoryService interface {
	// LockInventory acquires an optimistic or pessimistic lock and decrements available stock for the item.
	// Returns ErrInventoryDepleted if insufficient units are available.
	LockInventory(ctx context.Context, itemID string, quantity int) error

	// ReleaseInventory releases a previous stock lock and increments available stock upon transaction failure.
	ReleaseInventory(ctx context.Context, itemID string, quantity int) error
}

// FraudService defines the contract for assessing transaction risk via ML scoring models or heuristic fallback rules.
type FraudService interface {
	// EvaluateRisk analyzes the order payload and returns a calculated RiskScore.
	// If the implementation times out or fails, the caller's policy fallback takes effect.
	EvaluateRisk(ctx context.Context, req OrderRequest) (RiskScore, error)
}

// LoyaltyService defines the contract for asynchronous non-blocking reward point accrual.
//
// As a non-critical soft dependency, operations on this interface should be executed
// asynchronously without blocking the primary critical transaction path.
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
