package main

import (
	"context"
	"fmt"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go-demo/checkout/pkg/downstream"
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
	if res.Status == "REVIEW_PENDING" {
		statusColor = ColorYellow
	} else if res.Status == "FAILED" || res.Status == "REJECTED" || err != nil {
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
	fmt.Printf("Demonstrating Time Budgeting, Policy Composition, and Single-Process Fault Tolerance\n")

	telemetry := checkout.NewInMemoryTelemetry()
	payPolicyCfg := checkout.DefaultPaymentPolicyConfig(telemetry)
	fraudPolicyCfg := checkout.FraudPolicyConfig{
		Timeout:   100 * time.Millisecond,
		Telemetry: telemetry,
	}

	// -------------------------------------------------------------------------
	// SCENARIO 1: Happy Path
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 1: Happy Path Execution (Healthy Dependencies)")
	payGateway1 := downstream.NewSimulatedPaymentGateway(70 * time.Millisecond)
	inventory1 := downstream.NewInventoryService(map[string]int{"item_sku_101": 50}, 20*time.Millisecond)
	fraud1 := downstream.NewFraudService(30 * time.Millisecond)
	loyalty1 := downstream.NewLoyaltyService()

	orch1 := checkout.NewOrchestrator(payGateway1, inventory1, fraud1, loyalty1, telemetry, payPolicyCfg, fraudPolicyCfg)

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
	payGateway2 := downstream.NewSimulatedPaymentGateway(70 * time.Millisecond)
	payGateway2.QueueFailures(checkout.ErrGatewayUnavailable) // 1st attempt fails

	orch2 := checkout.NewOrchestrator(payGateway2, inventory1, fraud1, loyalty1, telemetry, payPolicyCfg, fraudPolicyCfg)

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
	payGateway3 := downstream.NewSimulatedPaymentGateway(50 * time.Millisecond)
	for i := 0; i < 10; i++ {
		payGateway3.QueueFailures(checkout.ErrGatewayUnavailable)
	}

	orch3 := checkout.NewOrchestrator(payGateway3, inventory1, fraud1, loyalty1, telemetry, payPolicyCfg, fraudPolicyCfg)

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
		fmt.Printf("\n%s--> Dispatching Request %d [CB State: %s]...%s\n", ColorMagenta, i, orch3.CircuitBreaker().State().String(), ColorReset)
		res, err := orch3.ProcessOrder(context.Background(), req)
		printResult(res, err)
	}

	// -------------------------------------------------------------------------
	// SCENARIO 4: Semi-Critical Fraud Check Timeout -> Degrades to Rule Heuristics
	// -------------------------------------------------------------------------
	printHeader("SCENARIO 4: ML Fraud Model Latency Spike -> 100ms Timeout Fallback")
	payGateway4 := downstream.NewSimulatedPaymentGateway(50 * time.Millisecond)
	fraud4 := downstream.NewFraudService(300 * time.Millisecond) // ML model hangs for 300ms (> 100ms budget)

	orch4 := checkout.NewOrchestrator(payGateway4, inventory1, fraud4, loyalty1, telemetry, payPolicyCfg, fraudPolicyCfg)

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
	fmt.Printf("\n%s%sDemo execution completed successfully.%s\n\n", ColorBold, ColorGreen, ColorReset)
}
