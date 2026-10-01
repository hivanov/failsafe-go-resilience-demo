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
│  └───────────────────────────────────────────────────┘││
└───────────────────────────────────────────────────────────┘
```

---

## 3. Package Structure & Resilient Decorator Architecture

The codebase cleanly separates domain orchestration, resilience policies, and raw service drivers using the **Decorator Pattern**:

```
failsafe-go-demo/
├── cmd/
│   └── demo/
│       └── main.go                  # Composition root & 4 interactive scenarios
├── pkg/
│   ├── checkout/                    # Core domain & orchestration
│   │   ├── interfaces.go            # Clean domain interfaces & context rules
│   │   ├── models.go                # OrderRequest, OrderResult, OrderStatus enum
│   │   ├── orchestrator.go          # Streamlined business workflow orchestrator
│   │   ├── telemetry.go             # Prometheus & OpenTelemetry event recorder
│   │   ├── orchestrator_test.go     # Parameterized table tests & benchmarks
│   │   └── postgres_integration_test.go # Real PostgreSQL Testcontainers tests
│   ├── downstream/                  # Raw driver implementations & simulators
│   │   ├── payment_gateway.go       # Simulated payment gateway driver
│   │   ├── inventory_service.go     # In-memory inventory lock driver
│   │   ├── postgres_inventory.go    # Live PostgreSQL SQL row-locking driver
│   │   └── fraud_service.go         # Simulated ML fraud scoring service
│   └── policies/                    # Resilient Decorator implementations (failsafe-go)
│       ├── payment_policy.go        # ResilientPaymentGateway & 5-layer Policy Onion
│       ├── inventory_policy.go      # ResilientInventoryService & 150ms lock timeout
│       ├── fraud_policy.go          # ResilientFraudService & 100ms timeout + fallback
│       └── loyalty_policy.go        # ResilientLoyaltyService & async 200ms timeout
└── doc/                             # In-depth architectural documentation
    ├── README.md                    # Deep-dive documentation index
    ├── anti_patterns.md             # Detailed guide on 10 resilience anti-patterns
    ├── acid_monolith_architecture.md# Single ACID database architecture & trade-offs
    ├── distributed_transactions_sagas.md # Sagas, 2PC, rollbacks, and literature
    └── context_cancellation_best_practices.md # Go context & socket leak rules
```

### Key Source References:
- **Resilient Decorator Implementations:** [`pkg/policies/payment_policy.go`](pkg/policies/payment_policy.go), [`pkg/policies/inventory_policy.go`](pkg/policies/inventory_policy.go), [`pkg/policies/fraud_policy.go`](pkg/policies/fraud_policy.go), [`pkg/policies/loyalty_policy.go`](pkg/policies/loyalty_policy.go).
- **Domain Orchestrator:** [`pkg/checkout/orchestrator.go`](pkg/checkout/orchestrator.go) (cleanly delegates resilience to injected decorators).
- **Strongly-Typed Domain Models & Enums:** [`pkg/checkout/models.go`](pkg/checkout/models.go) (`OrderStatus`, `PaymentStatus`, `RiskDecision`).
- **Composition Root:** [`cmd/demo/main.go`](cmd/demo/main.go).

---

## 4. Common Anti-Patterns in Resilient System Design

*(For comprehensive architectural analysis, see [`doc/anti_patterns.md`](doc/anti_patterns.md))*

1. **Unbounded Retries (The Infinite Retry Loop):**
   - *Anti-Pattern:* Retrying forever or setting excessively high retry counts.
   - *Impact:* Saturated connection pools, locked goroutines, and cascading server crashes.
   - *Fix:* Cap retries to 1–3 attempts max and wrap them inside an `OverallOperationTimeout` policy ([`pkg/policies/payment_policy.go`](pkg/policies/payment_policy.go)).
2. **Retries Without Jitter (The Thundering Herd):**
   - *Anti-Pattern:* Deterministic backoff intervals without randomized variance.
   - *Impact:* Thousands of clients retry simultaneously, sending shock waves that knock recovering services offline.
   - *Fix:* Always apply randomized jitter (`WithJitterFactor(0.2)`).
3. **Retrying Non-Idempotent Operations (The Double-Charge Bug):**
   - *Anti-Pattern:* Blindly retrying mutating operations (`POST /charges`) on network timeout.
   - *Idempotency Defined:* An operation is idempotent if $f(f(x)) = f(x)$ (multiple applications yield the exact same state as one).
   - *Impact:* Network timeouts are ambiguous (the server may have processed the charge before dropping the packet). Retrying without idempotency keys causes double-charging.
   - *Fix:* Require unique client idempotency keys ([`pkg/checkout/models.go`](pkg/checkout/models.go)) deduplicated by the server.
4. **Lack of Alternative Strategies (No Fallback):**
   - *Anti-Pattern:* Failing the entire user request when a non-critical dependency slows down.
   - *Fix:* Implement graceful fallbacks (e.g., ML timeout degrading to rule heuristics in [`pkg/policies/fraud_policy.go`](pkg/policies/fraud_policy.go) or routing failed payments to a manual review queue).
5. **Untested Policy Code ("Wishful Thinking"):**
   - *Anti-Pattern:* Declaring policies without rigorous fault-injection tests.
   - *Fix:* Validate under `-race` with table-driven tests ([`pkg/checkout/orchestrator_test.go`](pkg/checkout/orchestrator_test.go)) and live containers ([`pkg/checkout/postgres_integration_test.go`](pkg/checkout/postgres_integration_test.go)).
6. **Ignoring the Critical Path:**
   - *Anti-Pattern:* Tuning retries on background tasks while leaving core database locks and payment calls unbudgeted.
   - *Fix:* Map the critical path, allocate strict budgets to hard dependencies, and move non-critical tasks (e.g. loyalty accrual) off the critical path.
7. **Basing Policies on "Hunches" Instead of Observability:**
   - *Anti-Pattern:* Guessing timeouts instead of measuring P95/P99 latency histograms. Setting a timeout below P95 causes a self-inflicted outage.
   - *Fix:* Drive timeout and retry thresholds using real telemetry ([`pkg/checkout/telemetry.go`](pkg/checkout/telemetry.go)).
8. **Context Disconnection & Socket Leaking:**
   - *Anti-Pattern:* Declaring timeout policies without passing `exec.Context()` to `http.Client` or database drivers.
   - *Fix:* Strictly propagate `exec.Context()` to terminate active TCP sockets immediately on cancellation ([`doc/context_cancellation_best_practices.md`](doc/context_cancellation_best_practices.md)).
9. **Blind / Catch-All Error Retries:**
   - *Anti-Pattern:* Retrying deterministic client errors (`400 Bad Request`, `401 Unauthorized`, `404 Not Found`).
   - *Fix:* Only retry transient, recoverable errors (`503 Service Unavailable`, `429 Too Many Requests`, socket drops).
10. **Cascading Shared Circuit Breakers:**
    - *Anti-Pattern:* Reusing a single circuit breaker across multiple distinct downstream endpoints.
    - *Fix:* Isolate circuit breaker state per failure domain.

---

## 5. Further Reading & Architectural Deep Dives

All comprehensive deep-dive guides, distributed transaction strategies, and curated literature references are indexed in **[`doc/README.md`](doc/README.md)**:

- 📖 **[Architectural Anti-Patterns in Resilient System Design](doc/anti_patterns.md)**
- 📖 **[Single-Database ACID Architecture vs. Distributed Microservices](doc/acid_monolith_architecture.md)**
- 📖 **[Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](doc/distributed_transactions_sagas.md)**
- 📖 **[Context Propagation & Socket Leak Prevention in Go](doc/context_cancellation_best_practices.md)**

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

## 7. Running the Parameterized & Testcontainers Test Suite

```bash
# Run unit & live Testcontainers integration tests with race detector
go test -count=1 -v -race ./...
```

Coverage includes:
- **Parameterized Test Matrix ([`pkg/checkout/orchestrator_test.go`](pkg/checkout/orchestrator_test.go)):** Normal execution, transient recovery, non-retryable 400 fast-fail, inventory rollback, and fraud degradation.
- **Circuit Breaker Fast-Fail Test:** Verifies immediate $< 1\text{ms}$ return without touching downstream sockets.
- **High-Concurrency Bulkhead Test:** 30 concurrent goroutines under `-race`.
- **Context Cancellation Propagation Test:** Sub-millisecond abort on client cancel.
- **Live PostgreSQL Testcontainer Integration Test ([`pkg/checkout/postgres_integration_test.go`](pkg/checkout/postgres_integration_test.go)):**
  - Spins up a real `postgres:16-alpine` container via `testcontainers-go`.
  - Executes schema creation, atomic row locking (`SELECT ... FOR UPDATE`), and real SQL stock deductions.
  - Verifies concrete transactional rollback in PostgreSQL when payment fails.
  - Tests 8 concurrent buyer transactions against real PostgreSQL verifying zero double-spending.
