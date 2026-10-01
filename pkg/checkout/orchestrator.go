package checkout

import (
	"context"
	"fmt"
	"time"
)

// Orchestrator coordinates the end-to-end checkout pipeline within the 800ms business SLA.
// It remains completely decoupled from low-level failsafe-go mechanics, delegating timeout budgeting,
// retries, circuit breaking, and fallbacks to the resilient implementations of its injected interfaces.
type Orchestrator struct {
	paymentGateway PaymentGateway
	inventorySvc   InventoryService
	fraudSvc       FraudService
	loyaltySvc     LoyaltyService
	telemetry      TelemetryRecorder
}

// NewOrchestrator initializes an Orchestrator with injected dependencies.
func NewOrchestrator(
	payment PaymentGateway,
	inventory InventoryService,
	fraud FraudService,
	loyalty LoyaltyService,
	telemetry TelemetryRecorder,
) *Orchestrator {
	return &Orchestrator{
		paymentGateway: payment,
		inventorySvc:   inventory,
		fraudSvc:       fraud,
		loyaltySvc:     loyalty,
		telemetry:      telemetry,
	}
}

// ProcessOrder executes the complete checkout pipeline for an incoming order:
//
// 1. Checks parent context cancellation.
// 2. Evaluates fraud risk via the resilient FraudService (encapsulating 100ms timeout & fallback).
// 3. Locks warehouse inventory via the resilient InventoryService (encapsulating 150ms timeout).
// 4. Charges payment gateway via the resilient PaymentGateway (encapsulating 400ms Policy Onion: Timeout, Retry, CB, Fallback).
// 5. Rolls back inventory if payment fails non-retryably.
// 6. Asynchronously grants loyalty points via the resilient LoyaltyService (encapsulating async 200ms timeout).
//
// Returns an OrderResult detailing final state, settlement references, and elapsed latency.
func (o *Orchestrator) ProcessOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	start := time.Now()

	// Check if parent context is already canceled before starting
	if err := ctx.Err(); err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  OrderStatusCanceled,
			Reason:  err.Error(),
			Elapsed: time.Since(start),
		}, err
	}

	// 1. Semi-Critical Dependency: Fraud Evaluation (encapsulates 100ms Operation Timeout & heuristic fallback)
	fraudResult, err := o.fraudSvc.EvaluateRisk(ctx, req)
	if err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  OrderStatusRejected,
			Reason:  fmt.Sprintf("fraud check error: %v", err),
			Elapsed: time.Since(start),
		}, err
	}

	if fraudResult.Decision == RiskDecisionReject {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  OrderStatusRejected,
			Reason:  "high risk fraud detected",
			Elapsed: time.Since(start),
		}, ErrFraudThreshold
	}

	// 2. Hard Critical Dependency: Inventory Lock (encapsulates 150ms Operation Timeout)
	if err := o.inventorySvc.LockInventory(ctx, req.ItemID, req.Quantity); err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  OrderStatusFailed,
			Reason:  fmt.Sprintf("inventory lock failed: %v", err),
			Elapsed: time.Since(start),
		}, err
	}

	// 3. Hard Critical Dependency: Payment Gateway Execution
	// (encapsulates 400ms Overall Operation Timeout, Retries with Jitter, Circuit Breaker, Fallback, 150ms Per-Attempt Timeout)
	payReq := PaymentRequest{
		OrderID:        req.OrderID,
		Amount:         req.Amount,
		Currency:       req.Currency,
		IdempotencyKey: req.Idempotency,
	}
	paymentResp, err := o.paymentGateway.Charge(ctx, payReq)
	elapsed := time.Since(start)

	if err != nil {
		// Rollback inventory on non-retryable payment failure
		_ = o.inventorySvc.ReleaseInventory(context.Background(), req.ItemID, req.Quantity)
		return OrderResult{
			OrderID:  req.OrderID,
			Status:   OrderStatusFailed,
			Reason:   err.Error(),
			Elapsed:  elapsed,
			Attempts: paymentResp.Attempts,
		}, err
	}

	// Degraded fallback check (e.g. gateway down, order held in review queue)
	if paymentResp.Status == PaymentStatusReviewPending {
		return OrderResult{
			OrderID:       req.OrderID,
			Status:        OrderStatusReviewPending,
			TransactionID: paymentResp.TransactionID,
			Reason:        "Payment gateway degraded; routed to manual verification queue",
			Elapsed:       elapsed,
			Attempts:      paymentResp.Attempts,
			Degraded:      true,
		}, nil
	}

	// 4. Soft Non-Critical Dependency: Loyalty Points (encapsulates async 200ms Operation Timeout)
	if o.loyaltySvc != nil {
		go func() {
			_ = o.loyaltySvc.AccruePoints(context.Background(), req.CustomerID, req.Amount)
		}()
	}

	return OrderResult{
		OrderID:       req.OrderID,
		Status:        OrderStatusSuccess,
		TransactionID: paymentResp.TransactionID,
		Elapsed:       elapsed,
		Attempts:      paymentResp.Attempts,
		Degraded:      fraudResult.IsHeuristic,
	}, nil
}
