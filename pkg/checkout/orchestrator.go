package checkout

import (
	"context"
	"fmt"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
)

// Orchestrator coordinates the end-to-end checkout pipeline within the 800ms business SLA.
// It orchestrates hard critical dependencies (Payment, Inventory) and soft dependencies (Fraud, Loyalty)
// using composable failsafe-go policy executors, context propagation, and transactional inventory rollbacks.
type Orchestrator struct {
	paymentGateway PaymentGateway
	inventorySvc   InventoryService
	fraudSvc       FraudService
	loyaltySvc     LoyaltyService
	telemetry      TelemetryRecorder

	paymentExecutor   failsafe.Executor[PaymentResponse]
	paymentCB         circuitbreaker.CircuitBreaker[PaymentResponse]
	inventoryExecutor failsafe.Executor[any]
	fraudExecutor     failsafe.Executor[RiskScore]
	loyaltyExecutor   failsafe.Executor[any]
}

// NewOrchestrator initializes and returns a configured Orchestrator instance with
// interface-injected downstream dependencies and dedicated failsafe-go policy executors.
func NewOrchestrator(
	payment PaymentGateway,
	inventory InventoryService,
	fraud FraudService,
	loyalty LoyaltyService,
	telemetry TelemetryRecorder,
	policyCfg PaymentPolicyConfig,
	invCfg InventoryPolicyConfig,
	fraudCfg FraudPolicyConfig,
	loyaltyCfg LoyaltyPolicyConfig,
) *Orchestrator {
	pExec, cb := BuildPaymentExecutor(policyCfg)
	iExec := BuildInventoryExecutor(invCfg)
	fExec := BuildFraudExecutor(fraudCfg)
	lExec := BuildLoyaltyExecutor(loyaltyCfg)

	return &Orchestrator{
		paymentGateway:    payment,
		inventorySvc:      inventory,
		fraudSvc:          fraud,
		loyaltySvc:        loyalty,
		telemetry:         telemetry,
		paymentExecutor:   pExec,
		paymentCB:         cb,
		inventoryExecutor: iExec,
		fraudExecutor:     fExec,
		loyaltyExecutor:   lExec,
	}
}

// ProcessOrder executes the complete checkout pipeline for an incoming order:
//
// 1. Checks parent context cancellation.
// 2. Evaluates fraud risk within a 100ms Operation Timeout (degrading to heuristics on timeout).
// 3. Locks warehouse inventory within a 150ms Operation Timeout.
// 4. Charges payment gateway via the 400ms Policy Onion (Overall Operation Timeout -> Retry -> CB -> Per-Attempt Timeout).
// 5. Rolls back inventory if payment fails non-retryably.
// 6. Asynchronously grants loyalty points within a 200ms background Operation Timeout.
//
// Returns an OrderResult detailing final state, settlement references, attempt counts, and elapsed latency.
func (o *Orchestrator) ProcessOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	start := time.Now()

	// Check if parent context is already canceled before starting
	if err := ctx.Err(); err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  "CANCELED",
			Reason:  err.Error(),
			Elapsed: time.Since(start),
		}, err
	}

	// 1. Semi-Critical Dependency: Fraud Evaluation (Operation Timeout Policy: 100ms budget with fallback)
	fraudResult, err := o.fraudExecutor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[RiskScore]) (RiskScore, error) {
		return o.fraudSvc.EvaluateRisk(exec.Context(), req)
	})
	if err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  "REJECTED",
			Reason:  fmt.Sprintf("fraud check error: %v", err),
			Elapsed: time.Since(start),
		}, err
	}

	if fraudResult.Decision == "REJECT" {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  "REJECTED",
			Reason:  "high risk fraud detected",
			Elapsed: time.Since(start),
		}, ErrFraudThreshold
	}

	// 2. Hard Critical Dependency: Inventory Lock (Operation Timeout Policy: 150ms upper bound)
	err = o.inventoryExecutor.WithContext(ctx).RunWithExecution(func(exec failsafe.Execution[any]) error {
		return o.inventorySvc.LockInventory(exec.Context(), req.ItemID, req.Quantity)
	})
	if err != nil {
		return OrderResult{
			OrderID: req.OrderID,
			Status:  "FAILED",
			Reason:  fmt.Sprintf("inventory lock failed: %v", err),
			Elapsed: time.Since(start),
		}, err
	}

	// 3. Hard Critical Dependency: Payment Gateway Execution via Policy Onion
	// (Overall Operation Timeout: 400ms upper bound wrapping retries + per-attempt timeout: 150ms)
	var attempts int
	paymentResp, err := o.paymentExecutor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[PaymentResponse]) (PaymentResponse, error) {
		attempts = exec.Attempts()
		payReq := PaymentRequest{
			OrderID:        req.OrderID,
			Amount:         req.Amount,
			Currency:       req.Currency,
			IdempotencyKey: req.Idempotency,
		}
		// Pass exec.Context() so timeouts/cancellations propagate to downstream socket
		return o.paymentGateway.Charge(exec.Context(), payReq)
	})

	elapsed := time.Since(start)

	if err != nil {
		// Rollback inventory on non-retryable payment failure
		_ = o.inventorySvc.ReleaseInventory(context.Background(), req.ItemID, req.Quantity)
		return OrderResult{
			OrderID:  req.OrderID,
			Status:   "FAILED",
			Reason:   err.Error(),
			Elapsed:  elapsed,
			Attempts: attempts,
		}, err
	}

	// Degraded fallback check (e.g. gateway down, order held in review queue)
	if paymentResp.Status == "REVIEW_PENDING" {
		return OrderResult{
			OrderID:       req.OrderID,
			Status:        "REVIEW_PENDING",
			TransactionID: paymentResp.TransactionID,
			Reason:        "Payment gateway degraded; routed to manual verification queue",
			Elapsed:       elapsed,
			Attempts:      attempts,
			Degraded:      true,
		}, nil
	}

	// 4. Soft Non-Critical Dependency: Loyalty Points (Async Operation Timeout Policy: 200ms)
	if o.loyaltySvc != nil {
		go func() {
			_ = o.loyaltyExecutor.RunWithExecution(func(exec failsafe.Execution[any]) error {
				return o.loyaltySvc.AccruePoints(exec.Context(), req.CustomerID, req.Amount)
			})
		}()
	}

	return OrderResult{
		OrderID:       req.OrderID,
		Status:        "SUCCESS",
		TransactionID: paymentResp.TransactionID,
		Elapsed:       elapsed,
		Attempts:      attempts,
		Degraded:      fraudResult.IsHeuristic,
	}, nil
}

// CircuitBreaker returns the underlying payment circuit breaker for status inspection and metrics export.
func (o *Orchestrator) CircuitBreaker() circuitbreaker.CircuitBreaker[PaymentResponse] {
	return o.paymentCB
}
