package main

import (
	"context"
	"fmt"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go-demo/checkout/pkg/downstream"
	"github.com/failsafe-go-demo/checkout/pkg/policies"
)

// ANSI color formatting
const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorCyan    = "\033[36m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorRed     = "\033[31m"
	ColorMagenta = "\033[35m"
)

func printHeader(title string) {
	fmt.Printf("\n%s%s================================================================================%s\n", ColorBold, ColorCyan, ColorReset)
	fmt.Printf("%s%s  %s%s\n", ColorBold, ColorCyan, title, ColorReset)
	fmt.Printf("%s%s================================================================================%s\n", ColorBold, ColorCyan, ColorReset)
}

func printResult(res checkout.OrderResult, err error) {
	statusColor := ColorGreen
	if res.Status == checkout.OrderStatusReviewPending {
		statusColor = ColorYellow
	} else if res.Status == checkout.OrderStatusFailed || res.Status == checkout.OrderStatusRejected || err != nil {
		statusColor = ColorRed
	}

	fmt.Printf("  • %sOrder ID:%s     %s\n", ColorBold, ColorReset, res.OrderID)
	fmt.Printf("  • %sStatus:%s       %s%s%s\n", ColorBold, ColorReset, statusColor, res.Status, ColorReset)
	if res.TransactionID != "" {
		fmt.Printf("  • %sTransaction:%s  %s\n", ColorBold, ColorReset, res.TransactionID)
	}
	if res.Reason != "" {
		fmt.Printf("  • %sReason:%s       %s%s%s\n", ColorBold, ColorReset, ColorYellow, res.Reason, ColorReset)
	}
	fmt.Printf("  • %sElapsed:%s      %s%v%s (SLA Budget: 800ms)\n", ColorBold, ColorReset, ColorCyan, res.Elapsed.Round(time.Millisecond), ColorReset)
	fmt.Printf("  • %sAttempts:%s     %d\n", ColorBold, ColorReset, res.Attempts)
	fmt.Printf("  • %sDegraded:%s     %v\n", ColorBold, ColorReset, res.Degraded)
	if err != nil {
		fmt.Printf("  • %sError:%s        %s%v%s\n", ColorBold, ColorReset, ColorRed, err, ColorReset)
	}
}

func main() {
	fmt.Printf("%s%sFAILSAFE-GO ARCHITECTURE DEMO: 20-MINUTE LIVE SCENARIOS%s\n", ColorBold, ColorGreen, ColorReset)
	fmt.Printf("Demonstrating Operation Time Budgeting, Policy Composition, and Single-Process Fault Tolerance\n")

	telemetry := checkout.NewInMemoryTelemetry()
	payPolicyCfg := policies.DefaultPaymentPolicyConfig(telemetry)
	invPolicyCfg := policies.DefaultInventoryPolicyConfig(telemetry)
	fraudPolicyCfg := policies.DefaultFraudPolicyConfig(telemetry)
	loyaltyPolicyCfg := policies.LoyaltyPolicyConfig{OperationTimeout: 200 * time.Millisecond, Telemetry: telemetry}

	// -------------------------------------------------------------------------
	// SCENARIO 1: Happy Path
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 1: Happy Path Execution (Healthy Dependencies)")
	rawGateway1 := downstream.NewSimulatedPaymentGateway(70 * time.Millisecond)
	rawInventory1 := downstream.NewInventoryService(map[string]int{"item_sku_101": 50}, 20*time.Millisecond)
	rawFraud1 := downstream.NewFraudService(30 * time.Millisecond)
	rawLoyalty1 := downstream.NewLoyaltyService()

	// Decorate raw dependencies with resilient policy implementations
	pay1 := policies.NewResilientPaymentGateway(rawGateway1, payPolicyCfg)
	inv1 := policies.NewResilientInventoryService(rawInventory1, invPolicyCfg)
	fraud1 := policies.NewResilientFraudService(rawFraud1, fraudPolicyCfg)
	loyalty1 := policies.NewResilientLoyaltyService(rawLoyalty1, loyaltyPolicyCfg)

	orch1 := checkout.NewOrchestrator(pay1, inv1, fraud1, loyalty1, telemetry)

	req1 := checkout.OrderRequest{
		OrderID:     "ORD-2026-001",
		CustomerID:  "CUST-883",
		ItemID:      "item_sku_101",
		Quantity:    1,
		Amount:      129.99,
		Currency:    "EUR",
		Idempotency: "idem-key-001",
	}

	res1, err1 := orch1.ProcessOrder(context.Background(), req1)
	printResult(res1, err1)

	// -------------------------------------------------------------------------
	// SCENARIO 2: Transient 3rd-Party Glitch (Retry with Exponential Backoff + Jitter)
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 2: Transient Glitch (Attempt 1 fails 503 -> Retry 2 succeeds)")
	rawGateway2 := downstream.NewSimulatedPaymentGateway(70 * time.Millisecond)
	rawGateway2.QueueFailures(checkout.ErrGatewayUnavailable) // 1st attempt fails

	pay2 := policies.NewResilientPaymentGateway(rawGateway2, payPolicyCfg)
	orch2 := checkout.NewOrchestrator(pay2, inv1, fraud1, loyalty1, telemetry)

	req2 := checkout.OrderRequest{
		OrderID:     "ORD-2026-002",
		CustomerID:  "CUST-914",
		ItemID:      "item_sku_101",
		Quantity:    2,
		Amount:      259.98,
		Currency:    "EUR",
		Idempotency: "idem-key-002",
	}

	res2, err2 := orch2.ProcessOrder(context.Background(), req2)
	printResult(res2, err2)

	// -------------------------------------------------------------------------
	// SCENARIO 3: Downstream Total Outage & Circuit Breaker Trip + Graceful Fallback
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 3: Outage -> Circuit Breaker Opens -> Fast-Fail to Fallback")
	rawGateway3 := downstream.NewSimulatedPaymentGateway(50 * time.Millisecond)
	for i := 0; i < 10; i++ {
		rawGateway3.QueueFailures(checkout.ErrGatewayUnavailable)
	}

	pay3 := policies.NewResilientPaymentGateway(rawGateway3, payPolicyCfg)
	orch3 := checkout.NewOrchestrator(pay3, inv1, fraud1, loyalty1, telemetry)

	for i := 1; i <= 4; i++ {
		req := checkout.OrderRequest{
			OrderID:     fmt.Sprintf("ORD-2026-CB-%03d", i),
			CustomerID:  fmt.Sprintf("CUST-%d", 100+i),
			ItemID:      "item_sku_101",
			Quantity:    1,
			Amount:      99.00,
			Currency:    "EUR",
			Idempotency: fmt.Sprintf("idem-cb-%d", i),
		}
		fmt.Printf("\n%s--> Dispatching Request %d [CB State: %s]...%s\n", ColorMagenta, i, pay3.CircuitBreaker().State().String(), ColorReset)
		res, err := orch3.ProcessOrder(context.Background(), req)
		printResult(res, err)
	}

	// -------------------------------------------------------------------------
	// SCENARIO 4: Semi-Critical Fraud Check Timeout -> Degrades to Rule Heuristics
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 4: ML Fraud Model Latency Spike -> 100ms Timeout Fallback")
	rawGateway4 := downstream.NewSimulatedPaymentGateway(50 * time.Millisecond)
	rawFraud4 := downstream.NewFraudService(300 * time.Millisecond) // ML model hangs for 300ms (> 100ms budget)

	pay4 := policies.NewResilientPaymentGateway(rawGateway4, payPolicyCfg)
	fraud4 := policies.NewResilientFraudService(rawFraud4, fraudPolicyCfg)
	orch4 := checkout.NewOrchestrator(pay4, inv1, fraud4, loyalty1, telemetry)

	req4 := checkout.OrderRequest{
		OrderID:     "ORD-2026-004",
		CustomerID:  "CUST-772",
		ItemID:      "item_sku_101",
		Quantity:    1,
		Amount:      49.50,
		Currency:    "EUR",
		Idempotency: "idem-key-004",
	}

	res4, err4 := orch4.ProcessOrder(context.Background(), req4)
	printResult(res4, err4)

	// -------------------------------------------------------------------------
	// TELEMETRY SUMMARY
	// -------------------------------------------------------------------------
	printHeader("TELEMETRY & OBSERVABILITY AUDIT LOG")
	fmt.Printf("Total Retries Triggered:     %s%d%s\n", ColorYellow, telemetry.RetryCount, ColorReset)
	fmt.Printf("Total Timeouts Handled:      %s%d%s\n", ColorRed, telemetry.TimeoutCount, ColorReset)
	fmt.Printf("Total Fallback Executions:   %s%d%s\n", ColorCyan, telemetry.FallbackRuns, ColorReset)
	fmt.Printf("Final Circuit Breaker State: %s%s%s\n", ColorMagenta, telemetry.CBState, ColorReset)
	fmt.Printf("\nRecent Telemetry Event Stream:\n")
	for idx, evt := range telemetry.GetEvents() {
		if idx < 10 {
			fmt.Printf("  [%02d] %s\n", idx+1, evt)
		}
	}

	fmt.Printf("\n%sDemo execution completed successfully.%s\n", ColorGreen, ColorReset)
}
