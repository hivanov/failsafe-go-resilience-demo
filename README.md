# failsafe-go Architecture & Live Demo: SLA-Driven Fault Tolerance

> **Engineering for Reality: SLA-Driven Fault Tolerance in a Single Go Process**

This repository contains a production-grade demonstration, parameterized test suite, and live PostgreSQL Testcontainers suite showcasing resilience patterns using [`failsafe-go`](https://github.com/failsafe-go/failsafe-go) (official documentation at [failsafe-go.dev](https://failsafe-go.dev)).

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

## 4. The Monolithic Alternative: Plain Old "Everything in an ACID Database"

Before adopting distributed microservices, message brokers, outbox tables, and Sagas, software architects should always evaluate the **Plain Old ACID-Compliant Database Architecture** (Single Relational Database / Modular Monolith).

For the vast majority of real-world business applications (under 50,000–100,000 active concurrent users), housing the domain entities within a single ACID-compliant database (such as **PostgreSQL**, **MySQL/InnoDB**, or **CockroachDB**) completely eliminates the entire class of distributed transaction and rollback bugs.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       MODULAR MONOLITH + ACID DATABASE                      │
│                                                                             │
│  [ Go Process: Checkout Orchestrator ]                                      │
│    │                                                                        │
│    ├── BEGIN TRANSACTION (Read Committed / Serializable)                    │
│    │     1. SELECT stock FROM inventory WHERE item_id = $1 FOR UPDATE       │
│    │     2. UPDATE inventory SET stock = stock - $qty WHERE item_id = $1    │
│    │     3. INSERT INTO orders (id, customer_id, amount) VALUES (...)       │
│    │     4. [Call External Payment API via failsafe-go Policy Onion]        │
│    │        ├── On Success: COMMIT TRANSACTION (Instant atomic state)       │
│    │        └── On Failure: ROLLBACK TRANSACTION (Automatic 0.1ms rollback) │
│    └────────────────────────────────────────────────────────────────────────┘
```

### 4.1 Why Single-Database ACID Wins at Moderate Scale

1. **Instant, Zero-Code Rollbacks (`ROLLBACK`):**
   - The database engine's **Write-Ahead Log (WAL)** and **MVCC (Multi-Version Concurrency Control)** automatically undo all modified rows, sequence updates, and locks in under 0.1ms.
   - You do **not** need to write, test, or maintain inverse compensating functions ($C_i$).
2. **Deterministic Locking & Race Prevention (`SELECT ... FOR UPDATE`):**
   - Row-level pessimistic locking prevents inventory double-allocation without needing distributed locking layers like Redis Redlock or Consul locks.
3. **Zero Dual-Write Failures:**
   - No risk of state diverging between two disjoint databases or message queues.
4. **Sub-Millisecond In-Process Calls:**
   - Communication between modules (e.g. Order $\rightarrow$ Inventory $\rightarrow$ Customer) happens via in-memory Go function calls taking $< 1\mu\text{s}$, rather than network serialization and HTTP/gRPC round-trips.

---

### 4.2 Architecture Comparison Matrix

| Evaluation Dimension | Plain Old ACID Relational DB | Modular Monolith (1 DB) | Distributed Microservices (Sagas) |
|---|---|---|---|
| **Rollback Complexity** | **Trivial** (`ROLLBACK`) | **Trivial** (`ROLLBACK`) | **Very High** (Manual Compensating Actions $C_i$) |
| **Consistency Model** | **Immediate Consistency** (ACID) | **Immediate Consistency** (ACID) | **Eventual Consistency** (BASE) |
| **Failure Modes** | DB connection pool exhaustion | DB connection pool exhaustion | Partial partitions, dual writes, out-of-order events |
| **Latency per Step** | $< 1\text{ms}$ (Local DB lock/index) | $< 1\text{ms}$ (Local DB lock/index) | $20\text{ms} - 150\text{ms}$ per network hop |
| **Operational Footprint** | 1 Database instance (+ Replica) | 1 Database instance (+ Replica) | Kubernetes, Kafka/NATS, CDC Relay, Tracing |
| **Recommended Scale** | **Up to 50,000 DAU** | **Up to 500,000 DAU** | **Large Multi-Team Enterprise (> 1M DAU)** |

---

### 4.3 Go Implementation Example: Transactional Rollback with Statement Timeout

```go
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

type PostgresOrderRepository struct {
	db *sql.DB
}

func NewPostgresOrderRepository(db *sql.DB) *PostgresOrderRepository {
	return &PostgresOrderRepository{db: db}
}

// ExecuteAtomicCheckout executes stock locking, external payment, and order recording
// inside a single ACID transaction. If payment fails, the entire transaction is rolled back.
func (r *PostgresOrderRepository) ExecuteAtomicCheckout(
	ctx context.Context,
	req checkout.OrderRequest,
	chargeFn func(ctx context.Context) (checkout.PaymentResponse, error),
) (*checkout.OrderResult, error) {
	// 1. Bound transaction duration with context
	txCtx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()

	tx, err := r.db.BeginTx(txCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Defer rollback: If tx.Commit() is not reached, database guarantees 100% rollback
	defer tx.Rollback()

	// 2. Lock inventory row atomically with row-level lock
	var currentStock int
	row := tx.QueryRowContext(txCtx, "SELECT stock FROM inventory WHERE item_id = $1 FOR UPDATE", req.ItemID)
	if err := row.Scan(&currentStock); err != nil {
		return nil, fmt.Errorf("item not found: %w", err)
	}
	if currentStock < req.Quantity {
		return nil, checkout.ErrInventoryDepleted
	}

	// 3. Decrement stock
	_, err = tx.ExecContext(txCtx, "UPDATE inventory SET stock = stock - $1 WHERE item_id = $2", req.Quantity, req.ItemID)
	if err != nil {
		return nil, fmt.Errorf("failed to update inventory: %w", err)
	}

	// 4. Call external Payment Gateway (wrapped by failsafe-go policy onion)
	payResp, err := chargeFn(txCtx)
	if err != nil {
		// Returning error triggers defer tx.Rollback(), instantly restoring inventory stock
		return nil, fmt.Errorf("payment rejected, rolling back database: %w", err)
	}

	// 5. Record order entity
	_, err = tx.ExecContext(
		txCtx,
		"INSERT INTO orders (order_id, customer_id, amount, currency, status, tx_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		req.OrderID, req.CustomerID, req.Amount, req.Currency, payResp.Status, payResp.TransactionID, time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert order: %w", err)
	}

	// 6. Commit transaction atomically
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &checkout.OrderResult{
		OrderID:       req.OrderID,
		Status:        checkout.OrderStatusSuccess,
		TransactionID: payResp.TransactionID,
	}, nil
}
```

---

## 5. Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks

When systems exceed the scale of a single database, or when transactions span multiple external 3rd-party SaaS providers and independent microservices with disjoint databases, single ACID transactions are physically impossible.

### 5.1 The Distributed Transaction Trade-Off: 2PC vs. Sagas

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

### 5.2 The Saga Pattern: Forward Transactions ($T_i$) & Compensations ($C_i$)

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

### 5.3 Orchestrated vs. Choreographed Sagas

1. **Orchestrated Saga (Command-Driven):**
   - A central coordinator (such as our Go `Orchestrator` or an engine like **Temporal / Cadence**) explicitly invokes each service via RPC/HTTP and records state transitions. If an unrecoverable failure occurs, the orchestrator invokes compensating activities in reverse order.
   - *Best for:* Complex flows with strict SLA budgets, clear auditing requirements, and central time coordination.

2. **Choreographed Saga (Event-Driven):**
   - Services communicate by publishing and subscribing to domain events over a broker (Kafka, Solace, RabbitMQ, NATS).
   - E.g., `OrderService` emits `OrderCreated` $\rightarrow$ `InventoryService` consumes, locks stock, emits `InventoryLocked` $\rightarrow$ `PaymentService` consumes, charges card, emits `PaymentFailed` $\rightarrow$ `InventoryService` consumes `PaymentFailed` and executes compensation.
   - *Best for:* Simple pipelines with few participants and high decoupling needs.

---

### 5.4 The 4 Golden Rules of Distributed Rollbacks & Compensations

1. **Compensations MUST Be Idempotent:**
   - In distributed systems, network retries can deliver compensation commands multiple times. Executing $C_i$ (e.g., `ReleaseInventory`) 3 times must have the exact same effect as executing it once.
2. **Handle Out-of-Order Message Arrival:**
   - Due to network reordering, a compensation message ($C_i$) can arrive *before* the original forward command ($T_i$). Services must record a "cancel-pending" state so when $T_i$ eventually arrives, it is immediately discarded.
3. **Transactional Outbox Pattern (Dual-Write Prevention):**
   - Never write to a database and publish to a message broker as two separate, non-transactional operations. Store outgoing messages in a local database `outbox` table within the same ACID transaction as the business entity, and use a CDC (Change Data Capture) relay (e.g., Debezium) to publish to the broker.
4. **Compensations Cannot Fail (Must Retry to Completion):**
   - If a forward step fails, the business can abort. But if a *compensation* fails (e.g., DB temporarily down during inventory release), the system cannot give up. Compensations must be retried with exponential backoff until they succeed or are flagged for human operator intervention.

---

### 5.5 Go Implementation Example: Self-Contained Saga Coordinator with Automatic Compensation

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

### 5.6 Curated Deep-Dive Resources: Sagas & Distributed Rollbacks

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

## 6. Interface Decoupling & Resilient Decorator Architecture

To keep the top-level `Orchestrator` clean, decoupled, and testable, all resilience policies (timeouts, retries, circuit breakers, fallbacks) are encapsulated inside dedicated **Decorator implementations** of the domain interfaces:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       DECORATOR ARCHITECTURE IN GO                          │
│                                                                             │
│  [ Raw HTTP/DB Service ]                                                    │
│         │                                                                   │
│         ▼                                                                   │
│  [ Resilient Decorator Implementation ]                                     │
│    ├── failsafe-go Policy Onion (Operation Timeout, Retries, CB, Fallback)  │
│    └── Implements the clean domain interface (e.g. PaymentGateway)          │
│         │                                                                   │
│         ▼                                                                   │
│  [ Clean Orchestrator ]                                                     │
│    └── Focuses 100% on business workflow coordination (Zero failsafe clutter)│
└─────────────────────────────────────────────────────────────────────────────┘
```

### Resilient Decorator Wrappers (`pkg/policies/`):
- **`pkg/policies/payment_policy.go`** (`ResilientPaymentGateway`): Encapsulates the 5-layer Policy Onion (Fallback $\rightarrow$ 400ms Overall Timeout $\rightarrow$ Retries w/ Jitter $\rightarrow$ Circuit Breaker $\rightarrow$ 150ms Attempt Timeout).
- **`pkg/policies/inventory_policy.go`** (`ResilientInventoryService`): Encapsulates the 150ms database row-locking Operation Timeout.
- **`pkg/policies/fraud_policy.go`** (`ResilientFraudService`): Encapsulates the 100ms ML evaluation Operation Timeout and graceful heuristic fallback.
- **`pkg/policies/loyalty_policy.go`** (`ResilientLoyaltyService`): Encapsulates the 200ms background reward accrual Operation Timeout.

### Composition in Main / Setup:
```go
// 1. Raw infrastructure drivers (pkg/downstream)
rawPay := downstream.NewSimulatedPaymentGateway(70 * time.Millisecond)
rawInv := downstream.NewInventoryService(initialStock, 20 * time.Millisecond)
rawFraud := downstream.NewFraudService(30 * time.Millisecond)
rawLoyalty := downstream.NewLoyaltyService()

// 2. Decorate with resilient policy implementations (pkg/policies)
resilientPay := policies.NewResilientPaymentGateway(rawPay, payPolicyCfg)
resilientInv := policies.NewResilientInventoryService(rawInv, invPolicyCfg)
resilientFraud := policies.NewResilientFraudService(rawFraud, fraudPolicyCfg)
resilientLoyalty := policies.NewResilientLoyaltyService(rawLoyalty, loyaltyPolicyCfg)

// 3. Inject into Orchestrator (Orchestrator remains ultra-clean, pkg/checkout)
orchestrator := checkout.NewOrchestrator(resilientPay, resilientInv, resilientFraud, resilientLoyalty, telemetry)
```

---

## 7. Running the Interactive Demo

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

## 8. Running the Parameterized & Testcontainers Test Suite

```bash
# Run unit & live Testcontainers integration tests with race detector
go test -v -race ./...
```

Coverage includes:
- **Parameterized Test Matrix (`pkg/checkout/orchestrator_test.go`):** Normal execution, transient recovery, non-retryable 400 fast-fail, inventory rollback, and fraud degradation.
- **Circuit Breaker Fast-Fail Test:** Verifies immediate $< 1\text{ms}$ return without touching downstream sockets.
- **High-Concurrency Bulkhead Test:** 30 concurrent goroutines under `-race`.
- **Context Cancellation Propagation Test:** Sub-millisecond abort on client cancel.
- **Live PostgreSQL Testcontainer Integration Test (`pkg/checkout/postgres_integration_test.go`):**
  - Spins up a real `postgres:16-alpine` container via `testcontainers-go`.
  - Executes schema creation, atomic row locking (`SELECT ... FOR UPDATE`), and real SQL stock deductions.
  - Verifies concrete transactional rollback in PostgreSQL when payment fails.
  - Tests 8 concurrent buyer transactions against real PostgreSQL verifying zero double-spending.

---

## 9. Reference Documentation & Foundational Literature

- **Failsafe-go Official Site & Docs:** [failsafe-go.dev](https://failsafe-go.dev)
  - [Retry Policy Guide](https://failsafe-go.dev/retry)
  - [Circuit Breaker Guide](https://failsafe-go.dev/circuit-breaker)
  - [Fallback Policy Guide](https://failsafe-go.dev/fallback)
  - [Timeout Policy Guide](https://failsafe-go.dev/timeout)
- **Foundational Architecture Book:** *Release It! Design and Deploy Production-Ready Software (2nd Edition)* by Michael T. Nygard.
