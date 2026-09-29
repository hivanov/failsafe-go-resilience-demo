package checkout

import (
	"context"
)

// PaymentGateway defines the contract for processing 3rd-party credit card / debit transactions.
type PaymentGateway interface {
	Charge(ctx context.Context, req PaymentRequest) (PaymentResponse, error)
}

// InventoryService defines the contract for checking and locking warehouse stock.
type InventoryService interface {
	LockInventory(ctx context.Context, itemID string, quantity int) error
	ReleaseInventory(ctx context.Context, itemID string, quantity int) error
}

// FraudService defines the contract for ML/heuristic risk scoring.
type FraudService interface {
	EvaluateRisk(ctx context.Context, req OrderRequest) (RiskScore, error)
}

// LoyaltyService defines the contract for accrued points and loyalty programs (soft async dependency).
type LoyaltyService interface {
	AccruePoints(ctx context.Context, customerID string, amount float64) error
}

// TelemetryRecorder captures resilience policy events for Prometheus / OpenTelemetry export.
type TelemetryRecorder interface {
	RecordRetry(operation string, attempt int, err error)
	RecordCircuitBreakerState(operation string, state string)
	RecordTimeout(operation string, elapsedMs int64)
	RecordFallback(operation string, reason string)
}
