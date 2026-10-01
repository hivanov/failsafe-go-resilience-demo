# failsafe-go Architecture & Live Demo: SLA-Driven Fault Tolerance

> **Engineering for Reality: SLA-Driven Fault Tolerance in a Single Go Process**

This repository contains a production-grade demonstration and parameterized test suite showcasing resilience patterns using [`failsafe-go`](https://github.com/failsafe-go/failsafe-go) (official documentation at [failsafe-go.dev](https://failsafe-go.dev)).

---

## 1. Architectural Case Study: "Checkout Orchestrator"

- **Business SLA Contract:** Total HTTP round-trip **< 800ms** at **99.9%** availability.
- **Single Process Isolation:** Even in a single Go binary, outbound I/O (3rd-party APIs, database row locks, ML scoring) introduces distributed failure modes.

### Dependency Classification & Time Budget Allocation:

```
[ Total Business SLA: 800ms ]
  ├── Ingress & JSON Serialization Buffer: -100ms
  └── Available Working Budget: 700ms
        ├── [Hard Critical] 3rd-Party Payment Gateway: 400ms max (Includes up to 2 retries + backoff + jitter)
        ├── [Hard Critical] Inventory DB Row Lock:     150ms max
        ├── [Semi-Critical] ML Fraud Evaluation:        100ms max (Strict fallback to heuristic rules)
        └── [Safety Margin] Buffer:                     50ms slack
```

---

## 2. Policy Composition (The Execution Onion)

In `failsafe-go`, policies wrap from **Outer to Inner**:

```
[ Incoming Checkout Request ]
  │
  ▼
┌───────────────────────────────────────────────────────────┐
│ 1. Fallback Policy (Outermost Shield)                     │
│    Catches fatal errors & timeout breaches -> degraded    │
│  ┌───────────────────────────────────────────────────────┐│
│  │ 2. Overall Operation Timeout Policy (400ms Upper Bound)││
│  │    Caps cumulative duration across all retry attempts ││
│  │  ┌───────────────────────────────────────────────────┐││
│  │  │ 3. Retry Policy (Max 2 Retries, Backoff + Jitter) │││
│  │  │    Handles transient 503 / socket network drops   │││
│  │  │  ┌───────────────────────────────────────────────┐│││
│  │  │  │ 4. Circuit Breaker (3 failures in 10 attempts)││││
│  │  │  │    Fast-fails in < 1ms when gateway is dead   ││││
│  │  │  │  ┌───────────────────────────────────────────┐││││
│  │  │  │  │ 5. Per-Attempt Timeout Policy (150ms)     │││││
│  │  │  │  │    Caps single HTTP socket attempt        │││││
│  │  │  │  │  ┌─────────────────────────────────────┐  │││││
│  │  │  │  │  │ 6. Target Function (HTTP/gRPC Call) │  │││││
│  │  │  │  │  └─────────────────────────────────────┘  │││││
│  │  │  │  └───────────────────────────────────────────┘││││
│  │  │  └───────────────────────────────────────────────┘│││
│  │  └───────────────────────────────────────────────────┘││
│  └───────────────────────────────────────────────────────┘│
└───────────────────────────────────────────────────────────┘
```

---

## 3. Best Practice: Mandatory Context & Cancellation Support Across All Activities

### Architectural Mandate
In a high-throughput, fault-tolerant Go application, **every outbound dependency and activity interface MUST accept `context.Context` as its first parameter and strictly honor cancellation signals.**

Without context propagation, when a `failsafe-go` Operation Timeout or Per-Attempt Timeout fires:
- The orchestrator moves on, but the underlying goroutine and TCP socket remain active in the background.
- Memory buffers, connection pool slots, and file descriptors remain locked, leading to **silent resource exhaustion and cascading server crashes**.

### The 3 Rules of Context-Aware Resilience:
1. **Pass `exec.Context()` to Outbound Calls:** When invoking downstream services inside a failsafe executor, always pass `exec.Context()` down the call stack. Failsafe-go attaches its active attempt and overall timeouts to this context.
2. **Close Network Resources on Cancellation:** Use `http.NewRequestWithContext` or standard database drivers with `ExecContext`/`QueryContext` so that socket disconnects immediately abort in-flight TCP streams.
3. **Check `ctx.Done()` in Long-Running Operations:** For compute-heavy or batched activities, periodically check `ctx.Done()` or `ctx.Err()` to exit early before doing wasted computation.

### Example Service Implementation: Context-Aware HTTP Client

Here is an example demonstrating how a production downstream payment client should be structured:

```go
package downstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// HTTPPaymentGateway implements checkout.PaymentGateway with strict context support.
type HTTPPaymentGateway struct {
	client  *http.Client
	baseURL string
}

func NewHTTPPaymentGateway(baseURL string, transportTimeout time.Duration) *HTTPPaymentGateway {
	return &HTTPPaymentGateway{
		baseURL: baseURL,
		client: &http.Client{
			// Transport-level connection timeout (handshake/connect)
			Timeout: transportTimeout,
		},
	}
}

// Charge executes an external HTTP charge while strictly honoring context cancellation.
func (g *HTTPPaymentGateway) Charge(ctx context.Context, req checkout.PaymentRequest) (checkout.PaymentResponse, error) {
	// 1. Early exit check: Don't start work if context is already canceled/timed out
	if err := ctx.Err(); err != nil {
		return checkout.PaymentResponse{}, err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return checkout.PaymentResponse{}, fmt.Errorf("failed to serialize request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/charges", g.baseURL)

	// 2. Attach context to HTTP request:
	// When failsafe-go attempt timeout (e.g. 150ms) fires, ctx is canceled,
	// immediately terminating the active TCP connection and closing OS sockets.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return checkout.PaymentResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	// 3. Execute HTTP call
	resp, err := g.client.Do(httpReq)
	if err != nil {
		// If context was canceled or timed out during flight, return ctx.Err() directly
		if ctx.Err() != nil {
			return checkout.PaymentResponse{}, ctx.Err()
		}
		return checkout.PaymentResponse{}, checkout.ErrTransientNetwork
	}
	defer resp.Body.Close() // Ensure connection is returned to pool immediately

	// 4. Handle HTTP response status
	if resp.StatusCode == http.StatusServiceUnavailable {
		return checkout.PaymentResponse{}, checkout.ErrGatewayUnavailable
	} else if resp.StatusCode == http.StatusTooManyRequests {
		return checkout.PaymentResponse{}, checkout.ErrRateLimited
	} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return checkout.PaymentResponse{}, checkout.ErrInvalidPayment
	} else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return checkout.PaymentResponse{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var payResp checkout.PaymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payResp); err != nil {
		return checkout.PaymentResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}

	return payResp, nil
}
```

---

## 4. Interface Decoupling & Swappability

Every external dependency implements a distinct Go interface (`pkg/checkout/interfaces.go`):

- **`PaymentGateway`**: Interface for 3rd-party credit/debit charges.
- **`InventoryService`**: Interface for checking, locking, and releasing warehouse inventory.
- **`FraudService`**: Interface for ML risk evaluation and heuristic fallback.
- **`LoyaltyService`**: Interface for asynchronous non-blocking rewards.
- **`TelemetryRecorder`**: Interface for Prometheus metrics and OpenTelemetry span event hooks.

---

## 5. Running the Interactive Demo

To run the interactive CLI demo covering all 4 live presentation scenarios:

```bash
go run ./cmd/demo/main.go
```

### Live Scenarios Demonstrated:
1. **Scenario 1: Happy Path** — Fast ~120ms execution well within the 800ms SLA.
2. **Scenario 2: Transient 503 Glitch** — Attempt 1 fails, attempt 2 succeeds with backoff (~230ms < 400ms budget).
3. **Scenario 3: Outage & Circuit Breaker Trip** — Circuit trips to `OPEN`; subsequent requests fast-fail in < 1ms to graceful fallback (`REVIEW_PENDING`) without socket exhaustion.
4. **Scenario 4: ML Fraud Model Latency Spike** — 100ms timeout fires and degrades to heuristic rules without failing or delaying checkout.

---

## 6. Running the Parameterized Test Suite

```bash
go test -v -race ./...
```

Coverage includes:
- Parameterized matrix across normal execution, transient recovery, non-retryable 400 fast-fail, inventory rollback, and fraud degradation.
- Circuit breaker fast-fail verification.
- High-concurrency bulkhead isolation burst test under the Go race detector.
- Context cancellation propagation test.

---

## 7. Reference Documentation & Foundational Literature

- **Failsafe-go Official Site & Docs:** [failsafe-go.dev](https://failsafe-go.dev)
  - [Retry Policy Guide](https://failsafe-go.dev/retry)
  - [Circuit Breaker Guide](https://failsafe-go.dev/circuit-breaker)
  - [Fallback Policy Guide](https://failsafe-go.dev/fallback)
  - [Timeout Policy Guide](https://failsafe-go.dev/timeout)
- **Foundational Architecture Book:** *Release It! Design and Deploy Production-Ready Software (2nd Edition)* by Michael T. Nygard.
