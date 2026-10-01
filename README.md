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
        ├── [Hard Critical] Inventory DB Row Lock:     150ms max (Operation Timeout)
        ├── [Semi-Critical] ML Fraud Evaluation:        100ms max (Operation Timeout with heuristic fallback)
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
	// When failsafe-go attempt timeout (150ms) fires, ctx is canceled,
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

## 4. Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks

When a single transaction spans multiple independent microservices (e.g., Order Service, Payment Gateway, Inventory DB, Warehouse Fulfillment), traditional single-process database transactions (`BEGIN ... COMMIT`) do not exist.

### 4.1 The Distributed Transaction Trade-Off: 2PC vs. Sagas

```
┌──────────────────────────────────────┬──────────────────────────────────────┐
│ Two-Phase Commit (2PC / XA)          │ Saga Pattern (Compensating Sequence) │
├──────────────────────────────────────┼──────────────────────────────────────┤
│ ❌ Synchronous locks across network  │ ✔ Asynchronous local ACID TXs        │
│ ❌ Coordinator is single point of POF│ ✔ Highly available under network partition│
│ ❌ High latency (holds DB locks)     │ ✔ Fast forward execution             │
│ ❌ Poor horizontal scalability       │ ✔ Eventual consistency (BASE)        │
└──────────────────────────────────────┴──────────────────────────────────────┘
```

In high-throughput distributed systems, **Two-Phase Commit (2PC)** is widely considered an anti-pattern because holding locks across network hops destroys availability and throughput. Instead, architectures adopt **Eventual Consistency** via the **Saga Pattern**.

---

### 4.2 The Saga Pattern: Forward Transactions ($T_i$) & Compensations ($C_i$)

A Saga is a sequence of local transactions:
- For every forward step $T_i$ executed in a service, there is a corresponding **Compensating Transaction** $C_i$ designed to undo its semantic side-effects if a subsequent step $T_{i+1}$ permanently fails.

$$\text{Forward Flow: } T_1 \longrightarrow T_2 \longrightarrow T_3 \dots \longrightarrow T_n$$
$$\text{Rollback Flow (if } T_3 \text{ fails): } T_1 \longrightarrow T_2 \longrightarrow T_3 (\text{FAIL}) \Longrightarrow C_2 \longrightarrow C_1$$

#### In Our Checkout Service:
1. $T_1$ (`FraudService.EvaluateRisk`): Read-only evaluation (no compensation required).
2. $T_2$ (`InventoryService.LockInventory`): Decrements available stock from warehouse.
3. $T_3$ (`PaymentGateway.Charge`): Charges the customer credit card.
4. **Failure Trigger:** If $T_3$ fails with a non-retryable error (e.g. `ErrInvalidPayment` 400), the orchestrator immediately triggers **$C_2$ (`InventoryService.ReleaseInventory`)** to restore the locked stock.

---

### 4.3 Orchestrated vs. Choreographed Sagas

1. **Orchestrated Saga (Command-Driven):**
   - A central coordinator (such as our Go `Orchestrator` or an engine like **Temporal / Cadence**) explicitly invokes each service via RPC/HTTP and records state transitions. If an unrecoverable failure occurs, the orchestrator invokes compensating activities in reverse order.
   - *Best for:* Complex flows with strict SLA budgets, clear auditing requirements, and central time coordination.

2. **Choreographed Saga (Event-Driven):**
   - Services communicate by publishing and subscribing to domain events over a broker (Kafka, Solace, RabbitMQ, NATS).
   - E.g., `OrderService` emits `OrderCreated` $\rightarrow$ `InventoryService` consumes, locks stock, emits `InventoryLocked` $\rightarrow$ `PaymentService` consumes, charges card, emits `PaymentFailed` $\rightarrow$ `InventoryService` consumes `PaymentFailed` and executes compensation.
   - *Best for:* Simple pipelines with few participants and high decoupling needs.

---

### 4.4 The 4 Golden Rules of Distributed Rollbacks & Compensations

1. **Compensations MUST Be Idempotent:**
   - In distributed systems, network retries can deliver compensation commands multiple times. Executing $C_i$ (e.g., `ReleaseInventory`) 3 times must have the exact same effect as executing it once.
2. **Handle Out-of-Order Message Arrival:**
   - Due to network reordering, a compensation message ($C_i$) can arrive *before* the original forward command ($T_i$). Services must record a "cancel-pending" state so when $T_i$ eventually arrives, it is immediately discarded.
3. **Transactional Outbox Pattern (Dual-Write Prevention):**
   - Never write to a database and publish to a message broker as two separate, non-transactional operations. Store outgoing messages in a local database `outbox` table within the same ACID transaction as the business entity, and use a CDC (Change Data Capture) relay (e.g., Debezium) to publish to the broker.
4. **Compensations Cannot Fail (Must Retry to Completion):**
   - If a forward step fails, the business can abort. But if a *compensation* fails (e.g., DB temporarily down during inventory release), the system cannot give up. Compensations must be retried with exponential backoff until they succeed or are flagged for human operator intervention.

---

### 4.5 Go Implementation Example: Self-Contained Saga Coordinator with Automatic Compensation

Here is how an in-memory orchestrated Saga runner is structured in idiomatic Go:

```go
package saga

import (
	"context"
	"fmt"
)

// Step represents a forward activity and its inverse compensating rollback activity.
type Step struct {
	Name       string
	Execute    func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

// Orchestrator executes steps sequentially, rolling back completed steps in reverse order on failure.
type Orchestrator struct {
	steps []Step
}

func NewOrchestrator() *Orchestrator {
	return &Orchestrator{steps: make([]Step, 0)}
}

func (s *Orchestrator) AddStep(step Step) {
	s.steps = append(s.steps, step)
}

// Execute runs all forward steps. If any step fails, all preceding steps are compensated in LIFO order.
func (s *Orchestrator) Execute(ctx context.Context) error {
	var executedSteps []Step

	for _, step := range s.steps {
		if err := ctx.Err(); err != nil {
			s.rollback(context.Background(), executedSteps)
			return fmt.Errorf("saga canceled before '%s': %w", step.Name, err)
		}

		if err := step.Execute(ctx); err != nil {
			// Rollback all previously executed steps in reverse order
			s.rollback(context.Background(), executedSteps)
			return fmt.Errorf("step '%s' failed: %w", step.Name, err)
		}

		executedSteps = append(executedSteps, step)
	}

	return nil
}

func (s *Orchestrator) rollback(ctx context.Context, executed []Step) {
	// Traverse in reverse (LIFO)
	for i := len(executed) - 1; i >= 0; i-- {
		step := executed[i]
		if step.Compensate != nil {
			_ = step.Compensate(ctx)
		}
	}
}
```

---

### 4.6 Curated Deep-Dive Resources: Sagas & Distributed Rollbacks

#### Foundational Books:
- 📖 **"Microservices Patterns: With examples in Java"** by *Chris Richardson* (Manning Publications)
  - *Chapters 4 & 5:* The definitive guide on Sagas, Orchestration vs. Choreography, and Compensating Transactions.
- 📖 **"Designing Data-Intensive Applications (DDIA)"** by *Martin Kleppmann* (O'Reilly Media)
  - *Chapters 7, 8 & 9:* Deep analysis of Distributed Transactions, Dual-Writes, Atomic Commit, Linearizability, and Two-Phase Commit limitations.
- 📖 **"Building Microservices (2nd Edition)"** by *Sam Newman* (O'Reilly Media)
  - *Chapter 6:* Distributed Transactions, Sagas, Eventual Consistency, and Async Coordination.
- 📖 **"Enterprise Integration Patterns"** by *Gregor Hohpe & Bobby Woolf* (Addison-Wesley)
  - Covers Process Manager, Routing Slip, and Compensating Message Router patterns.

#### Seminal Research Papers:
- 📄 **"Sagas" (1987)** by *Hector Garcia-Molina & Kenneth Salem* (Princeton University)
  - The foundational paper introducing the Saga concept for long-lived transactions (LLTs). Available via ACM Digital Library.
- 📄 **"Life beyond Distributed Transactions: an Apostate’s Opinion" (2007)** by *Pat Helland* (Amazon / Microsoft)
  - Explains why distributed transactions fail to scale in cloud environments and how entities, idempotency, and messaging replace 2PC.

#### Production Go Distributed Transaction Frameworks & SDKs:
- 🛠️ **Temporal Go SDK** (`go.temporal.io/sdk`): [temporal.io](https://temporal.io)
  - The premier workflow-as-code orchestration engine in Go. Workflows automatically track activity state, persist execution histories, and trigger compensating activities upon failure.
- 🛠️ **DTM (Distributed Transaction Manager in Go)**: [github.com/dtm-labs/dtm](https://github.com/dtm-labs/dtm)
  - High-performance Go distributed transaction framework supporting **SAGA**, **TCC (Try-Confirm-Cancel)**, and **XA/2PC** with built-in sub-transaction barrier technology to prevent out-of-order execution and null compensations.
- 🛠️ **Cadence Go Client** (`go.uber.org/cadence`): [github.com/uber-go/cadence-client](https://github.com/uber-go/cadence-client)
  - Uber's distributed workflow orchestration engine for long-running, fault-tolerant business transactions.

---

## 5. Interface Decoupling & Swappability

Every external dependency implements a distinct Go interface (`pkg/checkout/interfaces.go`):

- **`PaymentGateway`**: Interface for 3rd-party credit/debit charges.
- **`InventoryService`**: Interface for checking, locking, and releasing warehouse inventory.
- **`FraudService`**: Interface for ML risk evaluation and heuristic fallback.
- **`LoyaltyService`**: Interface for asynchronous non-blocking rewards.
- **`TelemetryRecorder`**: Interface for Prometheus metrics and OpenTelemetry span event hooks.

---

## 6. Running the Interactive Demo

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

## 7. Running the Parameterized Test Suite

```bash
go test -v -race ./...
```

Coverage includes:
- Parameterized matrix across normal execution, transient recovery, non-retryable 400 fast-fail, inventory rollback, and fraud degradation.
- Circuit breaker fast-fail verification.
- High-concurrency bulkhead isolation burst test under the Go race detector.
- Context cancellation propagation test.

---

## 8. Reference Documentation & Foundational Literature

- **Failsafe-go Official Site & Docs:** [failsafe-go.dev](https://failsafe-go.dev)
  - [Retry Policy Guide](https://failsafe-go.dev/retry)
  - [Circuit Breaker Guide](https://failsafe-go.dev/circuit-breaker)
  - [Fallback Policy Guide](https://failsafe-go.dev/fallback)
  - [Timeout Policy Guide](https://failsafe-go.dev/timeout)
- **Foundational Architecture Book:** *Release It! Design and Deploy Production-Ready Software (2nd Edition)* by Michael T. Nygard.
