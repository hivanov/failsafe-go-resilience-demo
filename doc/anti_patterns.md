# Common Anti-Patterns in Resilient System Design

Building resilient software in Go requires understanding not just how to configure policies, but how poor configurations actively destabilize distributed systems. Below is an exhaustive breakdown of the most frequent anti-patterns encountered in production services.

---

## Table of Contents

- [1. Unbounded Retries (The Infinite Retry Loop)](#1-unbounded-retries-the-infinite-retry-loop)
- [2. Retries Without Jitter (The Thundering Herd / Synchronized Stampede)](#2-retries-without-jitter-the-thundering-herd--synchronized-stampede)
- [3. Retrying Non-Idempotent Operations (The Double-Charge Disaster)](#3-retrying-non-idempotent-operations-the-double-charge-disaster)
- [4. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)](#4-lack-of-alternative-strategies-binary-success-or-fail-thinking)
- [5. Untested Policy Code (Resilience as "Wishful Thinking")](#5-untested-policy-code-resilience-as-wishful-thinking)
- [6. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps](#6-ignoring-the-critical-path--over-optimizing-non-critical-steps)
- [7. Basing Policies on "Hunches" Instead of Observability & Metrics](#7-basing-policies-on-hunches-instead-of-observability--metrics)
- [8. Context Disconnection & Socket Leaking](#8-context-disconnection--socket-leaking)
- [9. Blind / Catch-All Error Retries (Retrying Deterministic Failures)](#9-blind--catch-all-error-retries-retrying-deterministic-failures)
- [10. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)](#10-cascading-circuit-breaker-trips-shared-breakers-across-disparate-endpoints)

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

## 4. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)
- **The Anti-Pattern:** Treating every dependency as binary: either it succeeds with 100% fidelity or the entire user request throws a 500 Internal Server Error.
- **The Failure Mode:** When a soft dependency (like an ML fraud scoring service, recommendation widget, or loyalty reward ledger) experiences a slow path, the entire critical checkout pipeline fails, leading to lost revenue and frustrated users.
- **Remediation:** Classify dependencies by business criticality. Equip all soft and semi-critical dependencies with **Fallback Policies**:
  - *Heuristic Fallback:* Degrade to fast rule-based risk checks when ML inference times out.
  - *Asynchronous Queue Fallback:* Route orders with unavailable payment gateways to a manual review queue (`REVIEW_PENDING`) rather than immediately aborting the customer session.
  - *Stale Cache Fallback:* Serve cached product catalog prices or static currency exchange rates.
- **Code Reference:** See [`pkg/policies/fraud_policy.go`](../pkg/policies/fraud_policy.go) (`ResilientFraudService`) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`ResilientPaymentGateway`).

---

## 5. Untested Policy Code (Resilience as "Wishful Thinking")
- **The Anti-Pattern:** Adding timeout, retry, and circuit breaker declarations into codebases without writing dedicated failure-injection and parameterized test suites.
- **The Failure Mode:** In production, policies fail silently:
  - Circuit breakers never open because the error types configured in `HandleErrors(...)` do not match the wrapped errors returned by the HTTP client.
  - Retry delays block request threads because timeouts were improperly layered.
  - Concurrency race conditions corrupt internal state.
- **Remediation:** Validate resilience policies using parameterized table tests, simulated network fault injection, live container integration tests (via `testcontainers-go`), and Go's concurrency race detector (`go test -race`).
- **Code Reference:** See [`pkg/checkout/orchestrator_test.go`](../pkg/checkout/orchestrator_test.go) (7 parameterized cases, circuit breaker fast-fail, bulkhead concurrency) and [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go).

---

## 6. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps
- **The Anti-Pattern:** Spending engineering time tuning retries on non-critical analytics or email dispatchers while leaving the core database lock and payment gateway unbudgeted.
- **The Failure Mode:** The user-facing latency budget is consumed by the wrong activities. Slow critical operations breach SLA while background work monopolizes worker resources.
- **Remediation:** Map the end-to-end request timeline. Identify the sequential **Critical Path** (e.g. Ingress $\rightarrow$ Fraud $\rightarrow$ Inventory DB Lock $\rightarrow$ Payment Charge). Allocate explicit time slices to each critical step. Move all non-critical work (e.g. Loyalty point calculation, email notification) off the critical path into asynchronous background goroutines bounded by separate contexts.
- **Code Reference:** See [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go) (sequential critical path followed by asynchronous `loyaltySvc.AccruePoints`).

---

## 7. Basing Policies on "Hunches" Instead of Observability & Metrics
- **The Anti-Pattern:** Picking arbitrary timeout and retry numbers (e.g. "let's set a 5-second timeout and 5 retries") without analyzing real telemetry.
- **The Failure Mode:**
  - *Timeout < P95 Latency:* If a downstream database query has a legitimate P95 latency of 300ms, setting a hunch-based timeout of 200ms causes a **self-inflicted outage** where 5% of healthy requests are aggressively aborted.
  - *Timeout > Client SLA:* Setting a 3-second timeout when the upstream API gateway SLA is 800ms means the upstream client disconnects long before the service gives up, wasting CPU and database capacity.
- **Remediation:** Instrument dependencies with OpenTelemetry and Prometheus histograms. Set timeout thresholds based on empirical data ($> \text{P99} + \text{buffer}$ for normal operations, strictly below total SLA budget).
- **Code Reference:** See [`pkg/checkout/telemetry.go`](../pkg/checkout/telemetry.go) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go).

---

## 8. Context Disconnection & Socket Leaking
- **The Anti-Pattern:** Declaring failsafe timeout policies but failing to pass `exec.Context()` to the underlying `http.Client` or `database/sql` driver.
- **The Failure Mode:** When the failsafe timeout fires, the orchestrator proceeds, but the background goroutine and network socket continue running in the background until the OS TCP timeout (often 2 minutes). Under high concurrency, connection pools and file descriptors become saturated, leading to **silent server death**.
- **Remediation:** Always pass `exec.Context()` down the entire call stack and use `http.NewRequestWithContext` or `QueryContext`/`ExecContext`.
- **Code Reference:** See [`doc/context_cancellation_best_practices.md`](./context_cancellation_best_practices.md) and [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go).

---

## 9. Blind / Catch-All Error Retries (Retrying Deterministic Failures)
- **The Anti-Pattern:** Retrying on *any* error (`HandleErrors(err)` or catching all HTTP non-200 responses).
- **The Failure Mode:** Retrying deterministic 4xx client errors (e.g., `400 Bad Request`, `401 Unauthorized`, `404 Not Found`, `422 Unprocessable Entity`, invalid credit card number). These errors will **never** succeed on retry; retrying them only wastes CPU, consumes bandwidth, and slows down the client response.
- **Remediation:** Only retry **transient, recoverable errors** (e.g. `503 Service Unavailable`, `429 Too Many Requests`, temporary network drops, connection resets).
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`HandleErrors(ErrTransientNetwork, ErrGatewayUnavailable, ErrRateLimited)`).

---

## 10. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)
- **The Anti-Pattern:** Using a single global circuit breaker instance to protect calls to multiple distinct external APIs or database tables.
- **The Failure Mode:** If an optional reporting endpoint goes down, the shared circuit breaker trips to `OPEN`, inadvertently taking down the critical payment processing pipeline with it.
- **Remediation:** Isolate circuit breakers per failure domain, per endpoint, or per microservice interface.
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) where the circuit breaker is strictly bound to `PaymentGateway`.
