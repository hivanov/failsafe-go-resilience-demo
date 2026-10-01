package checkout_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go-demo/checkout/pkg/downstream"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
)

func TestProcessOrder_ParameterizedTable(t *testing.T) {
	type testCase struct {
		name              string
		req               checkout.OrderRequest
		setupGateway      func(gw *downstream.SimulatedPaymentGateway)
		setupInventory    func(inv *downstream.ThreadSafeInventoryService)
		setupFraud        func(fr *downstream.SimulatedFraudService)
		expectedStatus    checkout.OrderStatus
		expectedDegraded  bool
		expectErr         bool
		minAttempts       int
		maxAttempts       int
		expectedInvRemain int
	}

	tests := []testCase{
		{
			name: "Case 1: Happy Path - Normal Success",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-001", CustomerID: "CUST-1", ItemID: "sku-apple", Quantity: 2, Amount: 100.0, Currency: "EUR", Idempotency: "idem-1",
			},
			setupGateway:      func(gw *downstream.SimulatedPaymentGateway) {},
			setupInventory:    func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud:        func(fr *downstream.SimulatedFraudService) {},
			expectedStatus:    checkout.OrderStatusSuccess,
			expectedDegraded:  false,
			expectErr:         false,
			minAttempts:       1,
			maxAttempts:       1,
			expectedInvRemain: 8,
		},
		{
			name: "Case 2: Transient Gateway 503 - Recovers on Attempt 2",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-002", CustomerID: "CUST-2", ItemID: "sku-apple", Quantity: 1, Amount: 50.0, Currency: "EUR", Idempotency: "idem-2",
			},
			setupGateway: func(gw *downstream.SimulatedPaymentGateway) {
				gw.QueueFailures(checkout.ErrGatewayUnavailable)
			},
			setupInventory:    func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud:        func(fr *downstream.SimulatedFraudService) {},
			expectedStatus:    checkout.OrderStatusSuccess,
			expectedDegraded:  false,
			expectErr:         false,
			minAttempts:       2,
			maxAttempts:       2,
			expectedInvRemain: 9,
		},
		{
			name: "Case 3: Non-Retryable Payment Rejection (400) - Fails Fast & Releases Inventory",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-003", CustomerID: "CUST-3", ItemID: "sku-apple", Quantity: 3, Amount: 150.0, Currency: "EUR", Idempotency: "idem-3",
			},
			setupGateway: func(gw *downstream.SimulatedPaymentGateway) {
				gw.QueueFailures(checkout.ErrInvalidPayment)
			},
			setupInventory:    func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud:        func(fr *downstream.SimulatedFraudService) {},
			expectedStatus:    checkout.OrderStatusFailed,
			expectedDegraded:  false,
			expectErr:         true,
			minAttempts:       1,
			maxAttempts:       1,
			expectedInvRemain: 10, // Must rollback locked inventory
		},
		{
			name: "Case 4: Persistent Outage - Fallback to Review Queue",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-004", CustomerID: "CUST-4", ItemID: "sku-apple", Quantity: 1, Amount: 30.0, Currency: "EUR", Idempotency: "idem-4",
			},
			setupGateway: func(gw *downstream.SimulatedPaymentGateway) {
				// Queue failures exceeding max retries (3 failures for 2 retries)
				gw.QueueFailures(checkout.ErrGatewayUnavailable, checkout.ErrGatewayUnavailable, checkout.ErrGatewayUnavailable)
			},
			setupInventory:    func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud:        func(fr *downstream.SimulatedFraudService) {},
			expectedStatus:    checkout.OrderStatusReviewPending,
			expectedDegraded:  true,
			expectErr:         false, // Degraded gracefully without returning hard error
			minAttempts:       3,
			maxAttempts:       3,
			expectedInvRemain: 9,
		},
		{
			name: "Case 5: ML Fraud Slow Path - Degrades to Heuristics without failing checkout",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-005", CustomerID: "CUST-5", ItemID: "sku-apple", Quantity: 1, Amount: 40.0, Currency: "EUR", Idempotency: "idem-5",
			},
			setupGateway:   func(gw *downstream.SimulatedPaymentGateway) {},
			setupInventory: func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud: func(fr *downstream.SimulatedFraudService) {
				fr.SetLatency(250 * time.Millisecond) // > 100ms fraud timeout
			},
			expectedStatus:    checkout.OrderStatusSuccess,
			expectedDegraded:  true, // Heuristic flag set
			expectErr:         false,
			minAttempts:       1,
			maxAttempts:       1,
			expectedInvRemain: 9,
		},
		{
			name: "Case 6: Fraud Hard Rejection - Aborts before payment or inventory lock",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-006", CustomerID: "CUST-6", ItemID: "sku-apple", Quantity: 1, Amount: 1000.0, Currency: "EUR", Idempotency: "idem-6",
			},
			setupGateway:   func(gw *downstream.SimulatedPaymentGateway) {},
			setupInventory: func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud: func(fr *downstream.SimulatedFraudService) {
				fr.SetOverride(95, checkout.RiskDecisionReject)
			},
			expectedStatus:    checkout.OrderStatusRejected,
			expectedDegraded:  false,
			expectErr:         true,
			minAttempts:       0, // Never touches payment gateway
			maxAttempts:       0,
			expectedInvRemain: 10,
		},
		{
			name: "Case 7: Insufficient Inventory - Fails fast without invoking Payment Gateway",
			req: checkout.OrderRequest{
				OrderID: "ORD-TEST-007", CustomerID: "CUST-7", ItemID: "sku-apple", Quantity: 999, Amount: 5000.0, Currency: "EUR", Idempotency: "idem-7",
			},
			setupGateway:      func(gw *downstream.SimulatedPaymentGateway) {},
			setupInventory:    func(inv *downstream.ThreadSafeInventoryService) {},
			setupFraud:        func(fr *downstream.SimulatedFraudService) {},
			expectedStatus:    checkout.OrderStatusFailed,
			expectedDegraded:  false,
			expectErr:         true,
			minAttempts:       0,
			maxAttempts:       0,
			expectedInvRemain: 10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			telemetry := checkout.NewInMemoryTelemetry()
			gw := downstream.NewSimulatedPaymentGateway(10 * time.Millisecond)
			inv := downstream.NewInventoryService(map[string]int{"sku-apple": 10}, 5*time.Millisecond)
			fr := downstream.NewFraudService(5 * time.Millisecond)
			loyalty := downstream.NewLoyaltyService()

			if tc.setupGateway != nil {
				tc.setupGateway(gw)
			}
			if tc.setupInventory != nil {
				tc.setupInventory(inv)
			}
			if tc.setupFraud != nil {
				tc.setupFraud(fr)
			}

			payCfg := checkout.DefaultPaymentPolicyConfig(telemetry)
			payCfg.BackoffMin = 10 * time.Millisecond
			payCfg.BackoffMax = 30 * time.Millisecond

			invCfg := checkout.DefaultInventoryPolicyConfig(telemetry)
			fraudCfg := checkout.DefaultFraudPolicyConfig(telemetry)
			loyaltyCfg := checkout.LoyaltyPolicyConfig{OperationTimeout: 200 * time.Millisecond, Telemetry: telemetry}

			// Wrap interfaces in their respective resilient policy implementations
			resilientPay := checkout.NewResilientPaymentGateway(gw, payCfg)
			resilientInv := checkout.NewResilientInventoryService(inv, invCfg)
			resilientFraud := checkout.NewResilientFraudService(fr, fraudCfg)
			resilientLoyalty := checkout.NewResilientLoyaltyService(loyalty, loyaltyCfg)

			orch := checkout.NewOrchestrator(resilientPay, resilientInv, resilientFraud, resilientLoyalty, telemetry)

			res, err := orch.ProcessOrder(context.Background(), tc.req)

			if tc.expectErr && err == nil {
				t.Fatalf("expected error, got nil result: %+v", res)
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Status != tc.expectedStatus {
				t.Errorf("expected status %s, got %s", tc.expectedStatus, res.Status)
			}
			if res.Degraded != tc.expectedDegraded {
				t.Errorf("expected degraded=%v, got %v", tc.expectedDegraded, res.Degraded)
			}
			if int(gw.TotalCalls()) < tc.minAttempts || int(gw.TotalCalls()) > tc.maxAttempts {
				t.Errorf("expected gateway calls between [%d, %d], got %d", tc.minAttempts, tc.maxAttempts, gw.TotalCalls())
			}
			if inv.GetStock("sku-apple") != tc.expectedInvRemain {
				t.Errorf("expected remaining inventory %d, got %d", tc.expectedInvRemain, inv.GetStock("sku-apple"))
			}
		})
	}
}

// TestCircuitBreaker_FastFail verifies that when the circuit opens, downstream calls are skipped completely.
func TestCircuitBreaker_FastFail(t *testing.T) {
	telemetry := checkout.NewInMemoryTelemetry()
	gw := downstream.NewSimulatedPaymentGateway(5 * time.Millisecond)
	inv := downstream.NewInventoryService(map[string]int{"sku-item": 500}, 2*time.Millisecond)
	fr := downstream.NewFraudService(2 * time.Millisecond)
	loyalty := downstream.NewLoyaltyService()

	payCfg := checkout.PaymentPolicyConfig{
		OverallOperationTimeout: 200 * time.Millisecond,
		AttemptTimeout:          80 * time.Millisecond,
		MaxRetries:              1,
		BackoffMin:              5 * time.Millisecond,
		BackoffMax:              10 * time.Millisecond,
		JitterFactor:            0.1,
		CBThreshold:             2,
		CBCapacity:              5,
		CBDelay:                 2 * time.Second,
		Telemetry:               telemetry,
	}

	invCfg := checkout.DefaultInventoryPolicyConfig(telemetry)
	fraudCfg := checkout.FraudPolicyConfig{OperationTimeout: 50 * time.Millisecond, Telemetry: telemetry}
	loyaltyCfg := checkout.LoyaltyPolicyConfig{OperationTimeout: 100 * time.Millisecond, Telemetry: telemetry}

	resilientPay := checkout.NewResilientPaymentGateway(gw, payCfg)
	resilientInv := checkout.NewResilientInventoryService(inv, invCfg)
	resilientFraud := checkout.NewResilientFraudService(fr, fraudCfg)
	resilientLoyalty := checkout.NewResilientLoyaltyService(loyalty, loyaltyCfg)

	orch := checkout.NewOrchestrator(resilientPay, resilientInv, resilientFraud, resilientLoyalty, telemetry)

	// Inject persistent errors to open circuit
	gw.QueueFailures(
		checkout.ErrGatewayUnavailable, checkout.ErrGatewayUnavailable,
		checkout.ErrGatewayUnavailable, checkout.ErrGatewayUnavailable,
	)

	// Request 1: Fails attempt 1 & retry 2 -> records 2 failures -> trips CB
	res1, _ := orch.ProcessOrder(context.Background(), checkout.OrderRequest{
		OrderID: "ORD-CB-1", ItemID: "sku-item", Quantity: 1, Amount: 10.0,
	})
	if res1.Status != checkout.OrderStatusReviewPending {
		t.Fatalf("expected REVIEW_PENDING, got %s", res1.Status)
	}

	callsAfterTrip := gw.TotalCalls()

	// Request 2 & 3: Should immediately fast-fail via Circuit Breaker without invoking Payment Gateway
	for i := 2; i <= 4; i++ {
		res, err := orch.ProcessOrder(context.Background(), checkout.OrderRequest{
			OrderID: fmt.Sprintf("ORD-CB-%d", i), ItemID: "sku-item", Quantity: 1, Amount: 10.0,
		})
		if err != nil {
			t.Fatalf("unexpected error on fast-fail fallback: %v", err)
		}
		if res.Status != checkout.OrderStatusReviewPending {
			t.Errorf("expected REVIEW_PENDING on open circuit, got %s", res.Status)
		}
	}

	if gw.TotalCalls() != callsAfterTrip {
		t.Errorf("circuit breaker failed to fast-fail: gateway calls increased from %d to %d", callsAfterTrip, gw.TotalCalls())
	}

	if resilientPay.CircuitBreaker().State() != circuitbreaker.OpenState {
		t.Errorf("expected circuit breaker state OPEN, got %v", resilientPay.CircuitBreaker().State())
	}
}

// TestConcurrency_Bulkhead_Isolation tests 30 concurrent orders under the race detector.
func TestConcurrency_Bulkhead_Isolation(t *testing.T) {
	telemetry := checkout.NewInMemoryTelemetry()
	gw := downstream.NewSimulatedPaymentGateway(15 * time.Millisecond)
	inv := downstream.NewInventoryService(map[string]int{"sku-bulk": 100}, 5*time.Millisecond)
	fr := downstream.NewFraudService(5 * time.Millisecond)
	loyalty := downstream.NewLoyaltyService()

	payCfg := checkout.DefaultPaymentPolicyConfig(telemetry)
	invCfg := checkout.DefaultInventoryPolicyConfig(telemetry)
	fraudCfg := checkout.DefaultFraudPolicyConfig(telemetry)
	loyaltyCfg := checkout.LoyaltyPolicyConfig{OperationTimeout: 200 * time.Millisecond, Telemetry: telemetry}

	resilientPay := checkout.NewResilientPaymentGateway(gw, payCfg)
	resilientInv := checkout.NewResilientInventoryService(inv, invCfg)
	resilientFraud := checkout.NewResilientFraudService(fr, fraudCfg)
	resilientLoyalty := checkout.NewResilientLoyaltyService(loyalty, loyaltyCfg)

	orch := checkout.NewOrchestrator(resilientPay, resilientInv, resilientFraud, resilientLoyalty, telemetry)

	const concurrency = 30
	var wg sync.WaitGroup
	var successCount int64

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			res, err := orch.ProcessOrder(context.Background(), checkout.OrderRequest{
				OrderID:     fmt.Sprintf("ORD-CONC-%03d", id),
				CustomerID:  fmt.Sprintf("CUST-%d", id),
				ItemID:      "sku-bulk",
				Quantity:    1,
				Amount:      25.0,
				Currency:    "EUR",
				Idempotency: fmt.Sprintf("idem-conc-%d", id),
			})
			if err == nil && res.Status == checkout.OrderStatusSuccess {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if atomic.LoadInt64(&successCount) != concurrency {
		t.Errorf("expected %d successful concurrent orders, got %d", concurrency, successCount)
	}
	if inv.GetStock("sku-bulk") != 100-concurrency {
		t.Errorf("expected remaining stock %d, got %d", 100-concurrency, inv.GetStock("sku-bulk"))
	}
}

// TestContext_Cancellation ensures active requests terminate immediately when parent context is canceled.
func TestContext_Cancellation(t *testing.T) {
	telemetry := checkout.NewInMemoryTelemetry()
	gw := downstream.NewSimulatedPaymentGateway(200 * time.Millisecond)
	inv := downstream.NewInventoryService(map[string]int{"sku-cancel": 10}, 10*time.Millisecond)
	fr := downstream.NewFraudService(10 * time.Millisecond)
	loyalty := downstream.NewLoyaltyService()

	payCfg := checkout.DefaultPaymentPolicyConfig(telemetry)
	invCfg := checkout.DefaultInventoryPolicyConfig(telemetry)
	fraudCfg := checkout.DefaultFraudPolicyConfig(telemetry)
	loyaltyCfg := checkout.LoyaltyPolicyConfig{OperationTimeout: 200 * time.Millisecond, Telemetry: telemetry}

	resilientPay := checkout.NewResilientPaymentGateway(gw, payCfg)
	resilientInv := checkout.NewResilientInventoryService(inv, invCfg)
	resilientFraud := checkout.NewResilientFraudService(fr, fraudCfg)
	resilientLoyalty := checkout.NewResilientLoyaltyService(loyalty, loyaltyCfg)

	orch := checkout.NewOrchestrator(resilientPay, resilientInv, resilientFraud, resilientLoyalty, telemetry)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	res, _ := orch.ProcessOrder(ctx, checkout.OrderRequest{
		OrderID: "ORD-CANCEL-1", ItemID: "sku-cancel", Quantity: 1, Amount: 10.0,
	})

	elapsed := time.Since(start)
	if elapsed > 150*time.Millisecond {
		t.Errorf("execution did not honor context cancellation promptly: took %v", elapsed)
	}
	if res.Status == checkout.OrderStatusSuccess {
		t.Errorf("expected non-success status on canceled context, got SUCCESS")
	}
}
