# Common Anti-Patterns in Resilient System Design

Building resilient software in Go requires understanding not just how to configure policies, but how flawed architectural assumptions and misconfigurations actively destabilize distributed systems. Below is an exhaustive breakdown of the most critical resilience anti-patterns encountered in production services.

---

## Table of Contents

- [1. Unbounded Retries (The Infinite Retry Loop)](#1-unbounded-retries-the-infinite-retry-loop)
- [2. Retries Without Jitter (The Thundering Herd / Synchronized Stampede)](#2-retries-without-jitter-the-thundering-herd--synchronized-stampede)
- [3. Retrying Non-Idempotent Operations (The Double-Charge Disaster)](#3-retrying-non-idempotent-operations-the-double-charge-disaster)
- [4. Distributed & In-Process Locking Pitfalls (+ Deadlocks)](#4-distributed--in-process-locking-pitfalls--deadlocks)
- [5. Inadequate Testing: Mock-Driven Illusion vs. Real Infrastructure](#5-inadequate-testing-mock-driven-illusion-vs-real-infrastructure)
- [6. Lack of Real-World Observations (Designing in a Telemetry Vacuum)](#6-lack-of-real-world-observations-designing-in-a-telemetry-vacuum)
- [7. Lack of Business Awareness (Building for Incorrect Scenarios & Load Profiles)](#7-lack-of-business-awareness-building-for-incorrect-scenarios--load-profiles)
- [8. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)](#8-lack-of-alternative-strategies-binary-success-or-fail-thinking)
- [9. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps](#9-ignoring-the-critical-path--over-optimizing-non-critical-steps)
- [10. Basing Policies on "Hunches" Instead of Empirical SLAs](#10-basing-policies-on-hunches-instead-of-empirical-slas)
- [11. Context Disconnection & Socket Leaking](#11-context-disconnection--socket-leaking)
- [12. Blind / Catch-All Error Retries (Retrying Deterministic Failures)](#12-blind--catch-all-error-retries-retrying-deterministic-failures)
- [13. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)](#13-cascading-circuit-breaker-trips-shared-breakers-across-disparate-endpoints)

---

## 1. Unbounded Retries (The Infinite Retry Loop)
- **The Anti-Pattern:** Retrying a failing call indefinitely (`WithMaxRetries(-1)` or an excessive count like 20) under the assumption that "it will eventually succeed."
- **The Failure Mode:** When a downstream service suffers an outage or severe degradation, infinite retry loops tie up caller goroutines, hold open file descriptors and TCP sockets, and multiply traffic by orders of magnitude. This turns a minor downstream hiccup into a **catastrophic cascading failure** that crashes the caller binary.
- **Remediation:** Always place a strict, low ceiling on retry counts (typically 1 to 3 attempts max). Wrap all retries inside an **Overall Operation Timeout** that bounds cumulative wall-clock time across all attempts combined.
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) for bounded retries (`MaxRetries: 2`) wrapped by `OverallOperationTimeout: 400ms`.

---

## 2. Retries Without Jitter (The Thundering Herd / Synchronized Stampede)
- **The Anti-Pattern:** Using fixed or deterministic exponential backoff intervals without adding randomized variance (jitter).
  $$\text{Deterministic Backoff: } t_{\text{retry}} = 100\text{ms}, 200\text{ms}, 400\text{ms}$$
- **The Failure Mode:** If 5,000 concurrent client requests fail simultaneously (e.g. during a brief network glitch or DB connection blip), all 5,000 clients will sleep for the exact same duration and fire their retry requests at the exact same millisecond. This creates periodic shock waves (**thundering herds**) that repeatedly smash the recovering downstream service back into an outage state.
- **Remediation:** Always inject a randomized jitter factor (e.g., 20% to 50% variance) into backoff calculations:
  $$t_{\text{retry}} = \text{backoff} \times (1 \pm \text{rand}(0, \text{jitter\_factor}))$$
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) configuring `WithJitterFactor(0.2)`.

---

## 3. Retrying Non-Idempotent Operations (The Double-Charge Disaster)
- **What is Idempotency?** An operation is **idempotent** if applying it multiple times produces the exact same side-effect and system state as applying it once:
  $$f(f(x)) = f(x)$$
  - *Idempotent operations:* `GET /items/123`, `PUT /users/456` (replacing state), `DELETE /sessions/789`, `SELECT ...`, `UPDATE inventory SET stock = 10 WHERE item_id = 'sku-1'`.
  - *Non-Idempotent operations:* `POST /charges` (creates a new financial transaction), `POST /orders` (inserts a new order row), `UPDATE accounts SET balance = balance - 100`.
- **The Anti-Pattern:** Blindly attaching retry policies to non-idempotent operations without client-provided idempotency keys.
- **The Failure Mode (Network Ambiguity):** When a network call times out (e.g., HTTP timeout or dropped TCP FIN/ACK packet), the caller **cannot distinguish** between two fundamentally different realities:
  1. Did the request fail *before* reaching the payment server? (Card not charged).
  2. Did the payment server successfully charge the card, but the network dropped the HTTP response on the way back? (Card charged).
  If the caller retries a raw non-idempotent `Charge()` call in case #2, the customer is **charged twice**.
- **Remediation:** Never retry mutating operations unless the target API supports unique **Idempotency Keys** (e.g., `Idempotency-Key: idem-order-12345`). The server must store the key in an atomic database table and deduplicate subsequent identical requests.
- **Code Reference:** See [`pkg/checkout/models.go`](../pkg/checkout/models.go) (`PaymentRequest.IdempotencyKey`) and [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go).

---

## 4. Distributed & In-Process Locking Pitfalls (+ Deadlocks)
- **The Anti-Pattern:** Holding in-memory mutexes (`sync.Mutex`) or database transaction row locks (`SELECT ... FOR UPDATE`) across external network boundaries, or acquiring multiple locks in non-deterministic order.
- **The Failure Modes:**
  1. **Lock-Network Coupling:** Acquiring a PostgreSQL row lock and then making an outbound HTTP call to a 3rd-party Payment Gateway inside the open transaction. If the payment gateway stalls for 5 seconds, that database row—and all incoming transactions attempting to access it—are frozen, exhausting the database connection pool.
  2. **Circular Deadlocks:** Service Worker 1 locks Resource A then requests Resource B ($A \rightarrow B$). Service Worker 2 locks Resource B then requests Resource A ($B \rightarrow A$). Both workers deadlock permanently until process termination or database deadlock detection aborts one.
  3. **Lock Starvation & Missing Timeouts:** Waiting indefinitely for a lock without a context deadline.
- **Remediation:**
  - **Never hold locks during network I/O:** Complete database operations before making network calls, or isolate network calls within separate bounded steps.
  - **Enforce Global Lock Ordering:** Always acquire locks in lexicographical or strictly defined sequence (e.g., sort item IDs before acquiring row locks).
  - **Mandate Lock Timeouts:** Set PostgreSQL statement timeouts (`SET LOCAL statement_timeout = '150ms'`) and use `context.WithTimeout` on all lock acquisition routines.
- **Code Reference:** See [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go) and [`doc/acid_monolith_architecture.md`](./acid_monolith_architecture.md).

---

## 5. Inadequate Testing: Mock-Driven Illusion vs. Real Infrastructure
- **The Anti-Pattern:** Testing resilience exclusively with synthetic in-memory mocks that immediately return hardcoded errors in $< 0.1\text{ms}$.
- **The Failure Mode:** Unit tests with shallow mocks give a false sense of security ("100% test coverage!"). In production, real distributed failure modes strike:
  - Database kernel row locks contend and deadlock under high concurrency.
  - Sockets leak because mocks never simulate half-open TCP connections or dropped packets.
  - Failsafe error filters (`HandleErrors`) fail to match wrapped `*net.OpError` or `*url.Error` types.
  - Concurrency data races corrupt circuit breaker counters under real multithreaded load.
- **Remediation:**
  - Author integration tests against **live containerized infrastructure** via `testcontainers-go` (e.g., real PostgreSQL 16 Alpine).
  - Always run tests with the Go race detector enabled: `go test -count=1 -v -race ./...`.
  - Simulate realistic latencies, socket disconnects, and transient error bursts using dedicated fault-injection simulators.
- **Code Reference:** See [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go) and [`pkg/checkout/orchestrator_test.go`](../pkg/checkout/orchestrator_test.go).

---

## 6. Lack of Real-World Observations (Designing in a Telemetry Vacuum)
- **The Anti-Pattern:** Designing timeout numbers, retry limits, and circuit breaker ratios purely on theoretical intuition or static architecture diagrams without inspecting production observability data.
- **The Failure Mode:**
  - Failure distributions in real networks are non-normal and heavily right-skewed (fat-tailed).
  - Designing for the "average latency" (P50) causes constant timeouts on the P95/P99 tail.
  - Engineering teams fail to observe how downstream dependencies degrade (e.g., partial service degradation vs. complete hard TCP resets).
- **Remediation:**
  - Instrument all policy execution hooks (`OnRetry`, `OnTimeoutExceeded`, `OnStateChanged`, `OnFallbackExecuted`) with Prometheus metrics and OpenTelemetry trace spans.
  - Base timeout thresholds on live P99 latency histograms under actual peak load.
- **Code Reference:** See [`pkg/checkout/telemetry.go`](../pkg/checkout/telemetry.go) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go).

---

## 7. Lack of Business Awareness (Building for Incorrect Scenarios & Load Profiles)
- **The Anti-Pattern:** Engineering complex, over-engineered architectures without understanding the business model, actual user behavior, traffic volatility, or the real financial cost of specific failure modes.
- **The Failure Modes:**
  1. **Premature Distributed Microservices:** Implementing 15 microservices with Sagas, Kafka event choreography, and outbox relays for a system handling 500 daily orders. The distributed coordination complexity costs $10\times$ more in operational overhead than a clean Modular Monolith backed by PostgreSQL.
  2. **Treating Auxiliary Features as Critical:** Failing the primary order checkout because the product recommendation engine or loyalty point calculation timed out.
  3. **Ignoring Flash-Sale Traffic Surges:** Designing timeouts for normal 10 req/sec steady state, then suffering total system collapse during Black Friday bursts when database lock contention jumps $50\times$.
- **Remediation:**
  - Align architectural complexity with actual business scale (apply the 80/20 Pareto rule).
  - Calculate the financial impact of degradation: It is far better to complete an order with estimated loyalty points or manual review queue routing than to abandon the customer's cart.
- **Code Reference:** See [`doc/design_process_and_modularity.md`](./design_process_and_modularity.md) and [`doc/acid_monolith_architecture.md`](./acid_monolith_architecture.md).

---

## 8. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)
- **The Anti-Pattern:** Treating every dependency as binary: either it succeeds with 100% fidelity or the entire user request throws a 500 Internal Server Error.
- **The Failure Mode:** When a soft dependency (like an ML fraud scoring service, recommendation widget, or loyalty reward ledger) experiences a slow path, the entire critical checkout pipeline fails, leading to lost revenue and frustrated users.
- **Remediation:** Classify dependencies by business criticality. Equip all soft and semi-critical dependencies with **Fallback Policies**:
  - *Heuristic Fallback:* Degrade to fast rule-based risk checks when ML inference times out.
  - *Asynchronous Queue Fallback:* Route orders with unavailable payment gateways to a manual review queue (`REVIEW_PENDING`) rather than immediately aborting the customer session.
  - *Stale Cache Fallback:* Serve cached product catalog prices or static currency exchange rates.
- **Code Reference:** See [`pkg/policies/fraud_policy.go`](../pkg/policies/fraud_policy.go) (`ResilientFraudService`) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`ResilientPaymentGateway`).

---

## 9. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps
- **The Anti-Pattern:** Spending engineering time tuning retries on non-critical analytics or email dispatchers while leaving the core database lock and payment gateway unbudgeted.
- **The Failure Mode:** The user-facing latency budget is consumed by the wrong activities. Slow critical operations breach SLA while background work monopolizes worker resources.
- **Remediation:** Map the end-to-end request timeline. Identify the sequential **Critical Path** (e.g. Ingress $\rightarrow$ Fraud $\rightarrow$ Inventory DB Lock $\rightarrow$ Payment Charge). Allocate explicit time slices to each critical step. Move all non-critical work (e.g. Loyalty point calculation, email notification) off the critical path into asynchronous background goroutines bounded by separate contexts.
- **Code Reference:** See [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go) (sequential critical path followed by asynchronous `loyaltySvc.AccruePoints`).

---

## 10. Basing Policies on "Hunches" Instead of Empirical SLAs
- **The Anti-Pattern:** Picking arbitrary timeout and retry numbers (e.g. "let's set a 5-second timeout and 5 retries") without analyzing real telemetry or upstream SLA contracts.
- **The Failure Mode:**
  - *Timeout < P95 Latency:* If a downstream database query has a legitimate P95 latency of 300ms, setting a hunch-based timeout of 200ms causes a **self-inflicted outage** where 5% of healthy requests are aggressively aborted.
  - *Timeout > Client SLA:* Setting a 3-second timeout when the upstream API gateway SLA is 800ms means the upstream client disconnects long before the service gives up, wasting CPU and database capacity.
- **Remediation:** Instrument dependencies with OpenTelemetry and Prometheus histograms. Set timeout thresholds based on empirical data ($> \text{P99} + \text{buffer}$ for normal operations, strictly below total SLA budget).
- **Code Reference:** See [`pkg/checkout/telemetry.go`](../pkg/checkout/telemetry.go) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go).

---

## 11. Context Disconnection & Socket Leaking
- **The Anti-Pattern:** Declaring failsafe timeout policies but failing to pass `exec.Context()` to the underlying `http.Client` or `database/sql` driver.
- **The Failure Mode:** When the failsafe timeout fires, the orchestrator proceeds, but the background goroutine and network socket continue running in the background until the OS TCP timeout (often 2 minutes). Under high concurrency, connection pools and file descriptors become saturated, leading to **silent server death**.
- **Remediation:** Always pass `exec.Context()` down the entire call stack and use `http.NewRequestWithContext` or `QueryContext`/`ExecContext`.
- **Code Reference:** See [`doc/context_cancellation_best_practices.md`](./context_cancellation_best_practices.md) and [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go).

---

## 12. Blind / Catch-All Error Retries (Retrying Deterministic Failures)
- **The Anti-Pattern:** Retrying on *any* error (`HandleErrors(err)` or catching all HTTP non-200 responses).
- **The Failure Mode:** Retrying deterministic 4xx client errors (e.g., `400 Bad Request`, `401 Unauthorized`, `404 Not Found`, `422 Unprocessable Entity`, invalid credit card number). These errors will **never** succeed on retry; retrying them only wastes CPU, consumes bandwidth, and slows down the client response.
- **Remediation:** Only retry **transient, recoverable errors** (e.g. `503 Service Unavailable`, `429 Too Many Requests`, temporary network drops, connection resets).
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`HandleErrors(ErrTransientNetwork, ErrGatewayUnavailable, ErrRateLimited)`).

---

## 13. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)
- **The Anti-Pattern:** Using a single global circuit breaker instance to protect calls to multiple distinct external APIs or database tables.
- **The Failure Mode:** If an optional reporting endpoint goes down, the shared circuit breaker trips to `OPEN`, inadvertently taking down the critical payment processing pipeline with it.
- **Remediation:** Isolate circuit breakers per failure domain, per endpoint, or per microservice interface.
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) where the circuit breaker is strictly bound to `PaymentGateway`.
