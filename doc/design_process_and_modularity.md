# Resilience Design Process & Modularity Principles in Go

This guide outlines the end-to-end engineering methodology for designing, prioritizing, and structuring fault-tolerant Go applications.

---

## Table of Contents

- [1. The 80/20 Pareto Rule in Resilience Engineering](#1-the-8020-pareto-rule-in-resilience-engineering)
- [2. The 6-Phase Iterative Design Process](#2-the-6-phase-iterative-design-process)
  - [Phase 1: Establish the Business SLA & User Contract](#phase-1-establish-the-business-sla--user-contract)
  - [Phase 2: Map & Classify Dependencies (Hard vs. Soft)](#phase-2-map--classify-dependencies-hard-vs-soft)
  - [Phase 3: Mathematical Time Budgeting](#phase-3-mathematical-time-budgeting)
  - [Phase 4: Policy Composition (The Outer-to-Inner Onion)](#phase-4-policy-composition-the-outer-to-inner-onion)
  - [Phase 5: Resilient Decorator Modularity](#phase-5-resilient-decorator-modularity)
  - [Phase 6: Empirical Verification & Telemetry Tuning Loop](#phase-6-empirical-verification--telemetry-tuning-loop)
- [3. Go Modularity & Interface Design Principles](#3-go-modularity--interface-design-principles)
  - [3.1 Interface Segregation & Single Responsibility](#31-interface-segregation--single-responsibility)
  - [3.2 The Decorator Pattern for Resilience Policies](#32-the-decorator-pattern-for-resilience-policies)
  - [3.3 Context-First Contracts](#33-context-first-contracts)
- [4. Summary Checklist for Engineering Teams](#4-summary-checklist-for-engineering-teams)

---

## 1. The 80/20 Pareto Rule in Resilience Engineering

A common failure mode in software engineering is attempting to "resilience-proof" every single line of code simultaneously. This leads to over-engineering, analysis paralysis, and unmaintainable complexity.

### The 80/20 Rule:
- **80% of business impact and revenue** comes from the core **Critical Path** (e.g. User Authentication $\rightarrow$ Cart Checkout $\rightarrow$ Payment Settlement).
- **80% of outages and latency spikes** are caused by a small handful ($< 20\%$) of unreliable outbound dependencies: 3rd-party SaaS HTTP APIs, database row locks under contention, and unbudgeted network serialization.

### Prioritization Strategy:
1. **Focus 80% of Resilience Effort on the Critical Path:** Strictly budget and protect the operations that directly impact the customer transaction.
2. **Isolate Soft Dependencies:** For the remaining 80% of auxiliary operations (recommendations, analytics, loyalty points, emails), ensure they are decoupled from the critical path so their failure **cannot** bring down the primary user flow.

---

## 2. The 6-Phase Iterative Design Process

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    THE ITERATIVE RESILIENCE DESIGN CYCLE                    │
│                                                                             │
│  [ 1. Business SLA ] ──► [ 2. Dependency Classification ]                   │
│                                    │                                        │
│  [ 4. Policy Onion ] ◄── [ 3. Mathematical Time Budget ]                    │
│         │                                                                   │
│  [ 5. Decorator Modularity ] ──► [ 6. Telemetry & Tuning Loop ]             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### Phase 1: Establish the Business SLA & User Contract
Before opening your IDE or choosing timeout values, establish the explicit business contract:
- What is the maximum acceptable user response latency? (e.g. **800ms total HTTP round-trip**).
- What happens if the response exceeds this threshold? (Users abandon carts, double-click, or trigger client-side timeouts).
- What is the availability target? (e.g. **99.9%**).

---

### Phase 2: Map & Classify Dependencies (Hard vs. Soft)
Enumerate every internal and external interaction involved in fulfilling the request:
- **Hard Critical Dependencies:** Without this step, the transaction cannot succeed. Must succeed or fail fast.
  - *Example:* 3rd-Party Payment Gateway, Warehouse Database Row Lock.
- **Semi-Critical Dependencies:** Necessary for optimal results, but acceptable to degrade to heuristic defaults under load or outage.
  - *Example:* Machine Learning Fraud Scoring Engine (degrades to rule-based heuristics on timeout).
- **Soft / Non-Critical Dependencies:** Auxiliary features that must never delay or block the primary transaction.
  - *Example:* Loyalty Point Accrual, Recommendation Widget, Audit Analytics (dispatched asynchronously).

---

### Phase 3: Mathematical Time Budgeting
Work **backward** from the total SLA to allocate bounded time slices.

$$\text{Total SLA: 800ms}$$
$$\text{Ingress / JSON Serialization Buffer: } -100\text{ms}$$
$$\text{Available Critical Path Budget: } 700\text{ms}$$

#### Pipeline Budget Allocation:
- **ML Fraud Evaluation (Semi-Critical):** $100\text{ms}$ max.
- **Inventory DB Row Lock (Hard Critical):** $150\text{ms}$ max.
- **Payment Gateway (Hard Critical):** $400\text{ms}$ max.
  - *Attempt 1 Timeout:* $150\text{ms}$
  - *Backoff + Jitter Delay:* $\sim 50\text{ms}$
  - *Attempt 2 (Retry) Timeout:* $150\text{ms}$
  - *Cumulative Payment Budget:* $150 + 50 + 150 = 350\text{ms} < 400\text{ms}$
- **Safety Margin Buffer:** $50\text{ms}$ slack.

$$\text{Total Formula: } 100\text{ms (Ingress)} + 100\text{ms (Fraud)} + 150\text{ms (Inventory)} + 400\text{ms (Payment)} + 50\text{ms (Slack)} = 800\text{ms}$$

---

### Phase 4: Policy Composition (The Outer-to-Inner Onion)
Assemble policies with deliberate wrapping hierarchy:
1. **Fallback (Outer):** Catches exhausted retries, open circuit breakers, and overall timeouts, returning degraded state (`REVIEW_PENDING`).
2. **Overall Operation Timeout:** Enforces the hard $400\text{ms}$ ceiling across all retries.
3. **Retry Policy:** Retries only transient errors ($503$, $429$, socket drops) with exponential backoff and randomized jitter.
4. **Circuit Breaker:** Fast-fails in $< 1\text{ms}$ when the downstream service is dead.
5. **Per-Attempt Timeout (Inner):** Binds each individual HTTP socket attempt to $150\text{ms}$.

---

### Phase 5: Resilient Decorator Modularity
Encapsulate all policy composition inside dedicated decorator implementations of domain interfaces. The core business `Orchestrator` remains 100% clean, decoupled, and focused purely on workflow sequencing.

---

### Phase 6: Empirical Verification & Telemetry Tuning Loop
Deploy with Prometheus and OpenTelemetry hooks:
- Inspect P95 and P99 latency histograms for each dependency.
- If P95 latency of the Payment Gateway increases from $70\text{ms}$ to $180\text{ms}$, adjust the per-attempt timeout and retry policy accordingly.
- Use chaos testing and live container integration tests (`testcontainers-go`) to verify that the system degrades gracefully under simulated partition and packet loss.

---

## 3. Go Modularity & Interface Design Principles

### 3.1 Interface Segregation & Single Responsibility
In Go, interfaces should be defined by the **consumer**, not the provider, and should be kept small and cohesive:

```go
// pkg/checkout/interfaces.go
type PaymentGateway interface {
    Charge(ctx context.Context, req PaymentRequest) (PaymentResponse, error)
}

type InventoryService interface {
    LockInventory(ctx context.Context, itemID string, quantity int) error
    ReleaseInventory(ctx context.Context, itemID string, quantity int) error
}

type FraudService interface {
    EvaluateRisk(ctx context.Context, req OrderRequest) (RiskScore, error)
}

type LoyaltyService interface {
    AccruePoints(ctx context.Context, customerID string, amount float64) error
}
```

---

### 3.2 The Decorator Pattern for Resilience Policies
Instead of littering the business orchestrator with `failsafe.Executor` calls, wrap raw drivers with resilient decorators in `pkg/policies/`:

```go
// pkg/policies/payment_policy.go
type ResilientPaymentGateway struct {
    inner          checkout.PaymentGateway
    executor       failsafe.Executor[checkout.PaymentResponse]
    circuitBreaker circuitbreaker.CircuitBreaker[checkout.PaymentResponse]
}

func (g *ResilientPaymentGateway) Charge(ctx context.Context, req checkout.PaymentRequest) (checkout.PaymentResponse, error) {
    return g.executor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[checkout.PaymentResponse]) (checkout.PaymentResponse, error) {
        resp, err := g.inner.Charge(exec.Context(), req)
        resp.Attempts = exec.Attempts()
        return resp, err
    })
}
```

#### Why This Modularity Wins:
- **Clean Business Logic:** The `Orchestrator` only coordinates business steps (`if err := o.inventorySvc.LockInventory(...)`).
- **Independent Testability:** Raw drivers, policy configurations, and domain orchestration can each be unit-tested in isolation without mocking frameworks.
- **Pluggability:** Swapping a simulated driver for an actual HTTP/REST client or gRPC stub requires zero changes to the orchestrator or policy layer.

---

### 3.3 Context-First Contracts
Every public method on every dependency interface **MUST accept `context.Context` as its first parameter**. Failsafe-go attaches its active timeouts and attempt deadlines to `exec.Context()`. Propagating this context guarantees that when a policy timeout expires, active TCP connections and underlying Goroutines terminate immediately.

---

## 4. Summary Checklist for Engineering Teams

| Check | Objective | Verification Method |
|---|---|---|
| **1. Business SLA Set** | Define total end-to-end response budget | Product & Engineering SLA contract (< 800ms) |
| **2. Math Budget Checked** | $\sum \text{timeouts} + \text{retries} + \text{backoffs} \le \text{SLA}$ | Static budget formula calculation |
| **3. Jitter Enabled** | Randomize retry backoff to prevent thundering herds | `WithJitterFactor(0.2)` in `retrypolicy` |
| **4. Idempotency Enforced** | Prevent duplicate charges during retries | Unique `Idempotency-Key` header & DB table |
| **5. Fallbacks Implemented** | Degrade soft dependencies gracefully | ML timeout $\rightarrow$ heuristics, payment down $\rightarrow$ review queue |
| **6. Context Propagated** | Ensure TCP sockets abort on timeout | `http.NewRequestWithContext` + `exec.Context()` |
| **7. Real Containers Tested** | Validate against live database row locks | `testcontainers-go` integration tests under `-race` |
| **8. Observability Hooked** | Track retry counts, CB state, and timeout spikes | Prometheus metrics & OpenTelemetry span events |
