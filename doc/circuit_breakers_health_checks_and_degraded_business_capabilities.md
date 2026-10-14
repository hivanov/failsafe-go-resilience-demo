# Circuit Breakers, Operation Health Checks & Degraded Business Capabilities

This document provides a comprehensive guide to circuit breaker mechanics, active vs. passive health probing, operation-specific health checks, telemetry integration, and the architectural mapping between technical degradation and business capabilities.

---

## Table of Contents

- [1. Circuit Breakers: From Simple Switches to Multi-Dimensional Sentinels](#1-circuit-breakers-from-simple-switches-to-multi-dimensional-sentinels)
  - [1.1 State Machine Dynamics: Closed, Open, Half-Open](#11-state-machine-dynamics-closed-open-half-open)
  - [1.2 The Cost of Not Breaking: Socket Starvation & Latency Cascades](#12-the-cost-of-not-breaking-socket-starvation--latency-cascades)
- [2. Sliding Window Mechanics: Count-Based vs. Time-Based Architecture](#2-sliding-window-mechanics-count-based-vs-time-based-architecture)
  - [2.1 Count-Based Sliding Window Mechanics](#21-count-based-sliding-window-mechanics)
  - [2.2 Time-Based Sliding Window Mechanics](#22-time-based-sliding-window-mechanics)
  - [2.3 Comprehensive Comparison: Count-Based vs. Time-Based](#23-comprehensive-comparison-count-based-vs-time-based)
  - [2.4 Sliding Windows in the HALF-OPEN State](#24-sliding-windows-in-the-half-open-state)
- [3. Active Synthetic Probes vs. Passive Rolling Execution Metrics](#3-active-synthetic-probes-vs-passive-rolling-execution-metrics)
  - [3.1 Passive Rolling Metrics (The Standard In-Process Breaker)](#31-passive-rolling-metrics-the-standard-in-process-breaker)
  - [3.2 Active Synthetic Probing (The Out-of-Band Health Scout)](#32-active-synthetic-probing-the-out-of-band-health-scout)
  - [3.3 Comparison Matrix: Passive vs. Active Probing](#33-comparison-matrix-passive-vs-active-probing)
- [4. Operation-Specific Health Checks](#4-operation-specific-health-checks)
  - [4.1 The Fallacy of the Monolithic `/healthz` Endpoint](#41-the-fallacy-of-the-monolithic-healthz-endpoint)
  - [4.2 Granular Operation Probes: Read vs. Write vs. Critical Checkout](#42-granular-operation-probes-read-vs-write-vs-critical-checkout)
  - [4.3 Isolated Circuit Breakers per Endpoint & Operation](#43-isolated-circuit-breakers-per-endpoint--operation)
- [5. Telemetry, Monitoring & Alerting for Circuit Breakers](#5-telemetry-monitoring--alerting-for-circuit-breakers)
  - [5.1 Essential Prometheus SLIs & Metrics](#51-essential-prometheus-slis--metrics)
  - [5.2 In-Code Telemetry Integration (`pkg/checkout/telemetry.go`)](#52-in-code-telemetry-integration-pkgcheckouttelemetrygo)
  - [5.3 Alerting Rules for Circuit State Changes & Flapping](#53-alerting-rules-for-circuit-state-changes--flapping)
  - [5.4 Correlating Breaker Trips with Downstream Dependencies](#54-correlating-breaker-trips-with-downstream-dependencies)
- [6. Technical Degradation Outcomes](#6-technical-degradation-outcomes)
  - [6.1 Microsecond Fast-Fail (< 1ms)](#61-microsecond-fast-fail--1ms)
  - [6.2 Load Shedding & Ingress Rejection](#62-load-shedding--ingress-rejection)
  - [6.3 Queue Redirection & Asynchronous Stashing](#63-queue-redirection--asynchronous-stashing)
- [7. Mapping Technical Degradation to Degraded Business Capabilities](#7-mapping-technical-degradation-to-degraded-business-capabilities)
  - [7.1 The Business Capability Criticality Matrix (BCCS Tiers)](#71-the-business-capability-criticality-matrix-bccs-tiers)
  - [7.2 Dynamic Feature Flagging & UX Degradation Plans](#72-dynamic-feature-flagging--ux-degradation-plans)
  - [7.3 Customer-Facing Impact & Revenue Containment Guide](#73-customer-facing-impact--revenue-containment-guide)
- [8. Complete Go Implementation: Multi-Operation Circuit Breaker with Telemetry](#8-complete-go-implementation-multi-operation-circuit-breaker-with-telemetry)

---

## 1. Circuit Breakers: From Simple Switches to Multi-Dimensional Sentinels

A Circuit Breaker is an automated stability pattern designed to prevent an application from repeatedly executing an operation that is almost certain to fail.

```
                  ┌──────────────────────────────┐
                  │            CLOSED            │
                  │   (Normal Traffic Flows)     │
                  └──────────────┬───────────────┘
                                 │
                   Failure Rate > Threshold (e.g. 50%)
                   OR Consecutive Failures >= N
                                 │
                                 ▼
                  ┌──────────────────────────────┐
                  │             OPEN             │
                  │   (Fast-Fail < 1ms to all)   │
                  └──────────────┬───────────────┘
                                 │
                   Trial Delay Expired (e.g. 10s)
                                 │
                                 ▼
                  ┌──────────────────────────────┐
                  │          HALF-OPEN           │
                  │   (Probe N Trial Requests)   │
                  └───────┬──────────────┬───────┘
                          │              │
       Trial Requests Fail│              │Trial Requests Succeed
                          ▼              ▼
                    [ Back to OPEN ]    [ Reset to CLOSED ]
```

### 1.1 State Machine Dynamics: Closed, Open, Half-Open
1. **CLOSED (Healthy)**: Requests execute normally. The breaker records execution outcomes (success/failure/timeout) across a rolling time window (e.g. 10s) or rolling count window (e.g. 100 executions).
2. **OPEN (Tripped / Broken)**: When the failure rate exceeds the threshold (e.g. 5 in 10 requests fail), the breaker trips to `OPEN`. All incoming requests are rejected **immediately ($< 1\text{ms}$)** without attempting network I/O or allocating sockets.
3. **HALF-OPEN (Canary Probing)**: After a configured trial duration (e.g. 15s), the breaker transitions to `HALF-OPEN`, allowing a controlled number of trial requests (e.g. 3 requests) through. If they succeed, the breaker resets to `CLOSED`; if any fail, it trips back to `OPEN`.

### 1.2 The Cost of Not Breaking: Socket Starvation & Latency Cascades
Without a circuit breaker:
- If a downstream dependency hangs for $5\text{s}$ before timing out, and incoming traffic is $200\text{ RPS}$, your service immediately accumulates **1,000 hanging threads/goroutines**.
- Memory explodes, the connection pool is starved, and the caller service crashes, creating a domino effect across the entire architecture.
- With a circuit breaker, $99.9\%$ of requests fail in $< 1\text{ms}$, preserving host memory and database connection pools.

---

## 2. Sliding Window Mechanics: Count-Based vs. Time-Based Architecture

A circuit breaker cannot make intelligent trip decisions based on a single request. It requires a statistical sample of recent execution outcomes. To measure failure ratios without unboundedly growing memory, circuit breakers employ **Sliding Windows**.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    SLIDING WINDOW TOPOLOGY COMPARISON                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ 1. COUNT-BASED SLIDING WINDOW (Fixed Ring Buffer of Size N)                 │
│                                                                             │
│  Slot:   [ 0 ] [ 1 ] [ 2 ] [ 3 ] [ 4 ] [ 5 ] [ 6 ] [ 7 ] [ 8 ] [ 9 ]        │
│  State:  [ ✔ ] [ ✔ ] [ ✘ ] [ ✘ ] [ ✔ ] [ ✘ ] [ ✔ ] [ ✔ ] [ ✘ ] [ ✘ ]        │
│                         ▲                                                   │
│                         └── Tail pointer overwrites oldest slot on each req │
│                                                                             │
│  • Window evaluated over the last N executions (e.g. N = 100).              │
│  • Failure Rate = (Failures in Window / N) * 100                            │
├─────────────────────────────────────────────────────────────────────────────┤
│ 2. TIME-BASED SLIDING WINDOW (Time-Bucketized Epoch Buffer)                 │
│                                                                             │
│  Bucket: | 1s | 2s | 3s | 4s | 5s | 6s | 7s | 8s | 9s | 10s|               │
│  Reqs:   | 20 | 25 | 18 | 30 | 22 | 19 | 24 | 28 | 31 | 15 | Total: 232    │
│  Fails:  |  1 |  0 |  8 | 15 | 18 | 12 | 20 | 25 | 28 | 14 | Fails: 141    │
│            ▲                                            ▲                   │
│            └── Oldest 1s bucket drops out (expired)    └── Newest 1s bucket │
│                                                                             │
│  • Window evaluated over the last T seconds (e.g. 10s divided into 10 x 1s).│
│  • Failure Rate = (Sum(Failures in Buckets) / Sum(Total in Buckets)) * 100 │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### 2.1 Count-Based Sliding Window Mechanics

In a Count-Based sliding window, the sample size is fixed to the last $N$ requests (e.g., $N = 100$).

#### 1. Ring Buffer Data Structure:
- Implemented as a circular array (ring buffer) of size $N$.
- Each slot stores a compact representation of the outcome (e.g., a bit: `1` for failure, `0` for success, or an integer enum).
- A write pointer tracks the current position. When a new execution finishes, the outcome is written to `buffer[head % N]`, overwriting the oldest recorded execution.
- A running counter of `total_failures` is updated in $O(1)$ time:
  $$\text{total\_failures} = \text{total\_failures} - \text{old\_slot\_value} + \text{new\_outcome}$$

#### 2. The Minimum Execution Count Guard ($N_{min}$):
- If $N = 100$ and threshold is $50\%$, what happens on startup when request #1 fails?
  - Without a guard: $\frac{1}{1} = 100\% \ge 50\% \implies$ **The circuit breaker trips immediately on the first request!**
- **The Guard Rule**: The circuit breaker evaluates the trip condition only if:
  $$\text{ExecutedRequests} \ge N_{min} \quad (\text{typically } 10\%–20\% \text{ of } N)$$

#### 3. Strengths & Pitfalls of Count-Based Windows:
- **Strength**: Instant responsiveness under high traffic; does not depend on system clock drift.
- **The Stale-Failure Trap**: If traffic volume drops sharply (e.g. at 03:00 AM), 5 failures that occurred 4 hours ago remain in the ring buffer indefinitely until new requests arrive to overwrite them.

---

### 2.2 Time-Based Sliding Window Mechanics

In a Time-Based sliding window, the sample size is bounded by a rolling duration $T$ (e.g. the last $10\text{ seconds}$).

#### 1. Time-Bucketized Ring Buffer Architecture:
- To avoid recording millions of individual timestamps in high-throughput systems ($100,000\text{ RPS}$), the time window $T$ is divided into $K$ discrete time buckets (e.g., a $10\text{s}$ window is split into $10$ buckets of $1\text{s}$ each, or $20$ buckets of $500\text{ms}$).
- Each bucket maintains two atomic counters: `total_requests` and `failed_requests`.
- As wall-clock time advances, the active bucket index rotates:
  $$\text{bucket\_index} = \left(\frac{\text{current\_timestamp\_ms}}{\text{bucket\_duration\_ms}}\right) \bmod K$$
- When the write head moves into a new bucket, that bucket's historical counts from the previous window epoch are zeroed out (reset).

#### 2. Dynamic Aggregation:
- At any point in time, the rolling metrics are computed by summing all $K$ active buckets:
  $$\text{RollingTotal} = \sum_{i=0}^{K-1} \text{bucket}[i].\text{total}$$
  $$\text{RollingFailures} = \sum_{i=0}^{K-1} \text{bucket}[i].\text{failures}$$
  $$\text{FailureRate} = \frac{\text{RollingFailures}}{\text{RollingTotal}} \times 100\%$$

#### 3. Strengths & Pitfalls of Time-Based Windows:
- **Strength**: Failures age out automatically based on the clock. Stale errors from an earlier transient blip disappear after $T$ seconds.
- **Pitfall**: Requires careful bucket granularity. If $K=1$ ($1$ bucket of $10\text{s}$), the metric will "jump" abruptly every 10 seconds. Using $K=10$ or $K=20$ produces smooth, continuous sliding metrics.

---

### 2.3 Comprehensive Comparison: Count-Based vs. Time-Based

| Dimension | Count-Based Sliding Window | Time-Based Sliding Window |
| :--- | :--- | :--- |
| **Window Boundary** | Fixed execution count (e.g. last 100 executions) | Fixed wall-clock duration (e.g. last 10 seconds) |
| **Memory Allocation** | Static circular array ($O(N)$ bits or bytes) | Static bucket array ($O(K)$ bucket structs) |
| **CPU Overhead** | $O(1)$ per request (increment pointer, atomic update) | $O(1)$ per request + background/epoch rotation |
| **Behavior Under Traffic Surge** | Rapidly evaluates surge; window represents a small time slice | Window duration stays constant; counts scale with RPS |
| **Behavior Under Low Traffic** | Retains historical failures until $N$ requests accumulate | Stale failures expire automatically as time elapses |
| **Ideal Use Case** | Synchronous RPCs, consistent request rate pipelines | Variable-load APIs, event handlers, webhook dispatches |

---

### 2.4 Sliding Windows in the HALF-OPEN State

When a circuit breaker transitions from `OPEN` to `HALF-OPEN`:
1. The historical sliding window is **completely cleared**.
2. A specialized, small **Trial Window** is initialized:
   - For Count-Based: A trial size $N_{trial}$ (e.g. 5 or 10 executions).
   - If $\ge N_{success\_threshold}$ (e.g. 9 out of 10) trial requests succeed, the breaker resets to `CLOSED`.
   - If **any** trial request fails (or if failures exceed the trial tolerance), the breaker trips immediately back to `OPEN` for another trial delay period.
3. This prevents a recovering dependency from being hammered by full production traffic while it is still fragile.

---

## 3. Active Synthetic Probes vs. Passive Rolling Execution Metrics

### 2.1 Passive Rolling Metrics (The Standard In-Process Breaker)
- **Mechanism**: The breaker listens to real customer requests as they execute through the `failsafe-go` pipeline.
- **Advantage**: Zero extra network overhead; observes true end-to-end production traffic.
- **Disadvantage**: When the breaker is `OPEN`, it stops receiving production traffic, making it dependent on the `HALF-OPEN` trial period (which exposes real users to probe failures).

### 2.2 Active Synthetic Probing (The Out-of-Band Health Scout)
- **Mechanism**: A dedicated background goroutine continuously sends lightweight synthetic ping/probe requests (e.g., every 2s) to the downstream dependency.
- **Advantage**: Detects downstream recovery without risking customer traffic.
- **Disadvantage**: Requires dedicated probe endpoints; synthetic probes may not exercise the full database/IO code path.

### 2.3 Comparison Matrix: Passive vs. Active Probing

| Dimension | Passive Rolling Breaker | Active Synthetic Probe | Hybrid Architecture (Best Practice) |
| :--- | :--- | :--- | :--- |
| **Traffic Source** | Real User Traffic | Synthetic Ping / Canary | User Traffic for Trip, Scout for Probe |
| **Overhead** | $0$ extra RPS | Constant background RPS | Negligible background probe |
| **Recovery Discovery** | Reactive (Half-Open Trial) | Proactive (Discovers before user) | Proactive background discovery |
| **Blast Radius** | User requests test Half-Open | Zero user impact during probe | Zero customer impact during probe |

---

## 3. Operation-Specific Health Checks

### 3.1 The Fallacy of the Monolithic `/healthz` Endpoint
A single global `/healthz` endpoint that returns `200 OK` if the process is alive is dangerously misleading:
- A database might be completely locked on writes (`READ ONLY` mode or disk full), while reads continue to succeed.
- A third-party payment gateway might be healthy for card authorizations but completely down for currency conversions.

### 3.2 Granular Operation Probes: Read vs. Write vs. Critical Checkout
Services must categorize and inspect health along specific operational axes:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       PAYMENT SERVICE HEALTH MATRIX                         │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
┌──────────────────────┐    ┌──────────────────────┐    ┌──────────────────────┐
│  READ PROBE (Cache)  │    │   CARD CHARGE PROBE  │    │  PAYPAL OAUTH PROBE  │
│                      │    │                      │    │                      │
│ Status: HEALTHY      │    │ Status: TRIPPED/OPEN │    │ Status: HEALTHY      │
│ Metric: 0.2ms latency│    │ Metric: 85% timeouts │    │ Metric: 120ms latency│
│ Action: Serve Read   │    │ Action: Fast-fail    │    │ Action: Route to     │
│         Catalog.     │    │         Credit Cards │    │         PayPal flow. │
└──────────────────────┘    └──────────────────────┘    └──────────────────────┘
```

### 3.3 Isolated Circuit Breakers per Endpoint & Operation
Never share a single Circuit Breaker instance across disparate endpoints. If `POST /v1/reports/export` is failing due to heavy database queries, its circuit breaker must **never** trip `POST /v1/checkout/charge`.

---

## 4. Telemetry, Monitoring & Alerting for Circuit Breakers

### 4.1 Essential Prometheus SLIs & Metrics
Every circuit breaker must expose 4 core Prometheus metrics:
1. **Current State Gauge** (`resilience_circuit_breaker_state`): Value 0 (Closed), 1 (Half-Open), or 2 (Open).
2. **State Transitions Counter** (`resilience_circuit_breaker_state_transitions_total`): Monotonic count of state changes over time to detect flapping and oscillation.
3. **Requests Served / Fast-Failed in Open State** (`resilience_circuit_breaker_open_rejections_total`): Total volume of user requests intercepted and aborted in $<1\text{ms}$ while the breaker is Open.
4. **Error Rate Percentage Gauge** (`resilience_circuit_breaker_error_rate_percentage`): Rolling percentage of failed executions within the active measurement window ($E = \frac{\text{Failures}}{\text{Total}} \times 100$).

```prometheus
# 1. Current State Gauge (0=Closed, 1=Half-Open, 2=Open)
# TYPE resilience_circuit_breaker_state gauge
resilience_circuit_breaker_state{dependency="payment_gateway", operation="charge"} 2

# 2. State Transitions Over Time
# TYPE resilience_circuit_breaker_state_transitions_total counter
resilience_circuit_breaker_state_transitions_total{dependency="payment_gateway", operation="charge"} 7

# 3. Requests Served / Fast-Failed in OPEN State
# TYPE resilience_circuit_breaker_open_rejections_total counter
resilience_circuit_breaker_open_rejections_total{dependency="payment_gateway", operation="charge"} 1842

# 4. Rolling Error Percentage (%)
# TYPE resilience_circuit_breaker_error_rate_percentage gauge
resilience_circuit_breaker_error_rate_percentage{dependency="payment_gateway", operation="charge"} 78.5
```

### 4.2 In-Code Telemetry Integration (`pkg/checkout/telemetry.go`)

In the Go implementation:
- `CBState`: String reflecting current state (`CLOSED`, `OPEN`, `HALF_OPEN`).
- `CBStateTransitionsCount`: Counter incremented whenever state changes.
- `CBRequestsFastFailedInOpen`: Map tracking fast-failed requests per operation.
- `GetCircuitBreakerErrorRate(operation)`: Returns real-time error percentage.

### 4.3 Alerting Rules for Circuit State Changes & Flapping

```yaml
# Prometheus Alertmanager Rule
groups:
  - name: resilience_alerts
    rules:
      - alert: CircuitBreakerTrippedOpen
        expr: resilience_circuit_breaker_state == 2
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "Circuit Breaker OPEN for {{ $labels.dependency }} ({{ $labels.operation }})"
          description: "Operation is fast-failing traffic. Degraded business capability in effect."

      - alert: CircuitBreakerFlapping
        expr: rate(resilience_circuit_breaker_trips_total[5m]) > 0.1
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "Circuit Breaker Flapping for {{ $labels.dependency }}"
          description: "Circuit is repeatedly opening and closing, indicating oscillating downstream instability."
```

---

## 5. Technical Degradation Outcomes

When an operation's circuit breaker trips to `OPEN`, the system executes one of three technical actions:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          CIRCUIT BREAKER IS OPEN                            │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
┌──────────────────────┐    ┌──────────────────────┐    ┌──────────────────────┐
│ 1. MICROSECOND FAST- │    │  2. LOAD SHEDDING /  │    │ 3. QUEUE REDIRECTION │
│         FAIL         │    │  INGRESS THROTTLING  │    │  & ASYNC STASHING    │
│                      │    │                      │    │                      │
│ Returns 503 / RFC7807│    │ Drops non-critical   │    │ Stashes request in   │
│ in < 1ms, saving     │    │ requests at API      │    │ durable SQS/Kafka    │
│ CPU and network.     │    │ Gateway level.       │    │ for delayed replay.  │
└──────────────────────┘    └──────────────────────┘    └──────────────────────┘
```

---

## 6. Mapping Technical Degradation to Degraded Business Capabilities

Resilience engineering must align technical circuit states with **Business Capability Degradation**.

```
┌─────────────────────────┐          ┌────────────────────────────────────────┐
│ TECHNICAL CIRCUIT STATE │          │      DEGRADED BUSINESS CAPABILITY      │
├─────────────────────────┤          ├────────────────────────────────────────┤
│ Fraud Service OPEN      │ ───────> │ Heuristic Rule Bypass (Accept Risk)    │
│ Recommendations OPEN    │ ───────> │ Static Bestseller Fallback             │
│ Inventory Service OPEN  │ ───────> │ Optimistic Backorder (Accept & Delay)  │
│ Primary Payment OPEN    │ ───────> │ Route to Secondary Gateway (Adyen)     │
│ Loyalty Points OPEN     │ ───────> │ Post-Checkout Asynchronous Grant       │
└─────────────────────────┘          └────────────────────────────────────────┘
```

### 6.1 The Business Capability Criticality Matrix (BCCS Tiers)

| Tier | Capability | Technical Dependency | Degraded Strategy when Tripped | Revenue / Customer Impact |
| :--- | :--- | :--- | :--- | :--- |
| **Tier 1** | Order Authorization | Payment Gateway | Failover to Secondary Gateway | **Zero** revenue loss ($< 0.1\%$ fee increase). |
| **Tier 2** | Real-Time Inventory | Inventory Database | Optimistic Reservation + Backorder | Low risk of overselling popular items. |
| **Tier 3** | Risk / Fraud Check | ML Scoring Engine | Fallback to Simple Regex Rules | Elevated chargeback risk accepted. |
| **Tier 4** | Recommendations | AI Recommendation API | Serve Cached Top 10 Bestsellers | Minor conversion rate dip (~$1.5\%$). |
| **Tier 5** | Loyalty Accrual | Loyalty Engine | Queue event in Kafka for 24h replay | **Zero** customer impact. |

### 6.2 Dynamic Feature Flagging & UX Degradation Plans
When a circuit trips, update in-memory feature flags to modify UI presentation:
- Hide "Live Stock Remaining" badges on product cards.
- Disable "Instant One-Click Checkout" and route users through standard checkout.
- Display a non-intrusive banner: *"High demand: Orders will be confirmed via email within 15 minutes."*

### 6.3 Customer-Facing Impact & Revenue Containment Guide
1. **Never present raw technical errors** (e.g. `503 Service Unavailable`, `CircuitBreakerOpenException`) to end users.
2. Provide a **degraded success promise** (e.g., `202 Accepted: Order Placed for Review`) whenever safe.
3. Contain financial loss by setting conservative spending limits during degraded operation.

---

## 7. Complete Go Implementation: Multi-Operation Circuit Breaker with Telemetry

```go
package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
)

var ErrDegradedFastFail = errors.New("operation circuit breaker is OPEN: fast-failing to preserve system capacity")

type OperationType string

const (
	OpPaymentCharge   OperationType = "payment_charge"
	OpInventoryLock   OperationType = "inventory_lock"
	OpFraudEvaluation OperationType = "fraud_evaluation"
)

type MultiBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[OperationType]circuitbreaker.CircuitBreaker[any]
}

func NewMultiBreakerRegistry() *MultiBreakerRegistry {
	r := &MultiBreakerRegistry{
		breakers: make(map[OperationType]circuitbreaker.CircuitBreaker[any]),
	}

	// Register isolated breakers per operation
	r.breakers[OpPaymentCharge] = circuitbreaker.Builder[any]().
		WithFailureThresholdRatio(3, 5).
		WithDelay(10 * time.Second).
		OnStateChanged(func(event circuitbreaker.StateChangedEvent) {
			recordCircuitMetric(string(OpPaymentCharge), event.NewState)
		}).
		Build()

	r.breakers[OpFraudEvaluation] = circuitbreaker.Builder[any]().
		WithFailureThresholdRatio(2, 4).
		WithDelay(5 * time.Second).
		OnStateChanged(func(event circuitbreaker.StateChangedEvent) {
			recordCircuitMetric(string(OpFraudEvaluation), event.NewState)
		}).
		Build()

	return r
}

func (r *MultiBreakerRegistry) Execute(ctx context.Context, op OperationType, fn func() (any, error)) (any, error) {
	r.mu.RLock()
	cb, exists := r.breakers[op]
	r.mu.RUnlock()

	if !exists {
		return fn()
	}

	executor := failsafe.NewExecutor[any](cb).WithContext(ctx)
	return executor.Get(fn)
}

func recordCircuitMetric(operation string, state circuitbreaker.State) {
	// Integrates with Prometheus gauge
	// 0: Closed, 1: Half-Open, 2: Open
}
```
