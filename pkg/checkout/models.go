// Package checkout provides domain models, decoupled interfaces, fault-tolerance policies,
// and orchestrators for executing resilient, SLA-governed checkout transactions in Go.
package checkout

import (
	"errors"
	"time"
)

// Standard domain errors returned by checkout operations and downstream dependencies.
var (
	// ErrTransientNetwork indicates a temporary socket timeout or network disconnection that is safe to retry.
	ErrTransientNetwork = errors.New("transient network timeout / socket error")

	// ErrGatewayUnavailable indicates an external 3rd-party HTTP 503 Service Unavailable response.
	ErrGatewayUnavailable = errors.New("downstream 3rd-party gateway 503 unavailable")

	// ErrRateLimited indicates an HTTP 429 Too Many Requests response from an external provider.
	ErrRateLimited = errors.New("downstream 429 rate limit exceeded")

	// ErrInvalidPayment indicates a non-retryable payment failure, such as invalid credentials or card rejection (400 Bad Request).
	ErrInvalidPayment = errors.New("non-retryable payment rejection (400 / invalid credentials)")

	// ErrInventoryDepleted indicates that the requested item is out of stock in warehouse storage.
	ErrInventoryDepleted = errors.New("insufficient inventory stock")

	// ErrFraudThreshold indicates that the transaction's ML/heuristic fraud score exceeded the acceptable risk threshold.
	ErrFraudThreshold = errors.New("fraud score exceeded acceptable risk threshold")
)

// OrderStatus defines the strongly-typed lifecycle state of an order processing result.
type OrderStatus string

const (
	// OrderStatusSuccess indicates the order was authorized, inventory reserved, and completed.
	OrderStatusSuccess OrderStatus = "SUCCESS"

	// OrderStatusReviewPending indicates payment degraded to an asynchronous manual review queue (fallback).
	OrderStatusReviewPending OrderStatus = "REVIEW_PENDING"

	// OrderStatusFailed indicates an unrecoverable failure during processing (e.g. payment rejected, db down).
	OrderStatusFailed OrderStatus = "FAILED"

	// OrderStatusRejected indicates the order was rejected before charge (e.g. fraud score too high).
	OrderStatusRejected OrderStatus = "REJECTED"

	// OrderStatusCanceled indicates processing was aborted due to client context cancellation.
	OrderStatusCanceled OrderStatus = "CANCELED"
)

// String returns the string representation of the OrderStatus.
func (s OrderStatus) String() string {
	return string(s)
}

// IsSuccessful returns true if the order completed successfully.
func (s OrderStatus) IsSuccessful() bool {
	return s == OrderStatusSuccess
}

// IsDegraded returns true if the order completed via fallback degradation.
func (s OrderStatus) IsDegraded() bool {
	return s == OrderStatusReviewPending
}

// PaymentStatus defines the status returned by payment processing gateways.
type PaymentStatus string

const (
	// PaymentStatusSettled indicates the payment transaction was authorized and settled.
	PaymentStatusSettled PaymentStatus = "SETTLED"

	// PaymentStatusReviewPending indicates the payment was routed to manual review queue.
	PaymentStatusReviewPending PaymentStatus = "REVIEW_PENDING"

	// PaymentStatusRejected indicates the payment was rejected by the issuer.
	PaymentStatusRejected PaymentStatus = "REJECTED"
)

// String returns the string representation of PaymentStatus.
func (s PaymentStatus) String() string {
	return string(s)
}

// RiskDecision defines the categorical outcome of a fraud evaluation.
type RiskDecision string

const (
	// RiskDecisionApprove indicates the transaction is low risk and approved.
	RiskDecisionApprove RiskDecision = "APPROVE"

	// RiskDecisionManualReview indicates the transaction requires human inspection.
	RiskDecisionManualReview RiskDecision = "MANUAL_REVIEW"

	// RiskDecisionReject indicates the transaction is high risk and rejected.
	RiskDecisionReject RiskDecision = "REJECT"

	// RiskDecisionApproveDegraded indicates the transaction was approved via fallback heuristics.
	RiskDecisionApproveDegraded RiskDecision = "APPROVE_DEGRADED"
)

// String returns the string representation of RiskDecision.
func (d RiskDecision) String() string {
	return string(d)
}

// OrderRequest represents the incoming customer checkout request.
type OrderRequest struct {
	// OrderID is the unique identifier for the purchase order.
	OrderID string

	// CustomerID is the identifier of the authenticated buyer.
	CustomerID string

	// ItemID is the SKU of the product to purchase.
	ItemID string

	// Quantity is the number of units to reserve and purchase.
	Quantity int

	// Amount is the total transaction cost.
	Amount float64

	// Currency is the 3-letter ISO currency code (e.g., "EUR", "USD").
	Currency string

	// Idempotency is the client-provided idempotency key preventing duplicate charges.
	Idempotency string
}

// OrderResult represents the final outcome of the checkout transaction returned to the caller.
type OrderResult struct {
	// OrderID is the echoed purchase order identifier.
	OrderID string `json:"order_id"`

	// Status indicates the final strongly-typed processing state.
	Status OrderStatus `json:"status"`

	// TransactionID is the settled payment reference or fallback queue identifier.
	TransactionID string `json:"transaction_id,omitempty"`

	// Reason contains human-readable explanation when the order fails or degrades.
	Reason string `json:"reason,omitempty"`

	// Elapsed is the total wall-clock time spent processing the request.
	Elapsed time.Duration `json:"elapsed"`

	// Attempts is the number of payment gateway attempts executed.
	Attempts int `json:"attempts"`

	// Degraded indicates whether any soft dependency (e.g. ML fraud or gateway fallback) operated in degraded mode.
	Degraded bool `json:"degraded"`
}

// PaymentRequest contains the payload dispatched to the 3rd-party payment gateway.
type PaymentRequest struct {
	// OrderID is the unique reference for the order.
	OrderID string

	// Amount is the monetary charge amount.
	Amount float64

	// Currency is the 3-letter ISO currency code.
	Currency string

	// IdempotencyKey prevents double-charging during retries.
	IdempotencyKey string
}

// PaymentResponse contains the result received from the 3rd-party payment gateway.
type PaymentResponse struct {
	// TransactionID is the unique settlement reference returned by the gateway.
	TransactionID string

	// Status is the gateway transaction state.
	Status PaymentStatus

	// ProcessedAt records the gateway settlement timestamp.
	ProcessedAt time.Time

	// Attempts records the number of gateway execution attempts.
	Attempts int
}

// RiskScore represents the evaluation output from the fraud analysis service.
type RiskScore struct {
	// Score is the evaluated risk rating from 0 (lowest risk) to 100 (highest risk).
	Score int

	// Decision represents the strongly-typed risk assessment outcome.
	Decision RiskDecision

	// Confidence is the statistical confidence score of the evaluation (0.0 to 1.0).
	Confidence float64

	// IsHeuristic indicates whether this evaluation was generated by rule heuristics due to an ML timeout.
	IsHeuristic bool
}
