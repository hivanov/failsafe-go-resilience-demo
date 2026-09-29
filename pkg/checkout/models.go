package checkout

import (
	"errors"
	"time"
)

// Standard Domain Errors
var (
	ErrTransientNetwork   = errors.New("transient network timeout / socket error")
	ErrGatewayUnavailable = errors.New("downstream 3rd-party gateway 503 unavailable")
	ErrRateLimited        = errors.New("downstream 429 rate limit exceeded")
	ErrInvalidPayment     = errors.New("non-retryable payment rejection (400 / invalid credentials)")
	ErrInventoryDepleted  = errors.New("insufficient inventory stock")
	ErrFraudThreshold     = errors.New("fraud score exceeded acceptable risk threshold")
)

// OrderRequest represents the incoming checkout transaction
type OrderRequest struct {
	OrderID     string
	CustomerID  string
	ItemID      string
	Quantity    int
	Amount      float64
	Currency    string
	Idempotency string
}

// OrderResult represents the final checkout result returned to caller
type OrderResult struct {
	OrderID       string        `json:"order_id"`
	Status        string        `json:"status"` // SUCCESS, REVIEW_PENDING, FAILED
	TransactionID string        `json:"transaction_id,omitempty"`
	Reason        string        `json:"reason,omitempty"`
	Elapsed       time.Duration `json:"elapsed"`
	Attempts      int           `json:"attempts"`
	Degraded      bool          `json:"degraded"`
}

// PaymentRequest contains payload sent to 3rd-party payment gateway
type PaymentRequest struct {
	OrderID        string
	Amount         float64
	Currency       string
	IdempotencyKey string
}

// PaymentResponse contains result from payment gateway
type PaymentResponse struct {
	TransactionID string
	Status        string
	ProcessedAt   time.Time
}

// RiskScore represents fraud check evaluation
type RiskScore struct {
	Score        int    // 0 to 100 (higher = riskier)
	Decision     string // APPROVE, MANUAL_REVIEW, REJECT
	Confidence   float64
	IsHeuristic  bool
}
