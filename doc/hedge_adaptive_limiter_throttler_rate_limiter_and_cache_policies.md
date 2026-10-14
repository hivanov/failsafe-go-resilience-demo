# Advanced Resilience Policies: Hedge, Adaptive Limiter, Adaptive Throttler, Rate Limiter & Cache

This document provides a comprehensive architectural and systems-engineering guide to the advanced policies in `failsafe-go`: **Hedge Policy**, **Adaptive Limiter (TCP Vegas / Gradient)**, **Adaptive Throttler (Google SRE Client-Side Throttling)**, **Rate Limiter (Smooth vs. Bursty)**, and **Cache Policy**.

---

## Table of Contents

- [1. The Complete Failsafe-Go Policy Taxonomy](#1-the-complete-failsafe-go-policy-taxonomy)
- [2. Hedge Policy: Speculative Concurrency for Long-Tail Latency Elimination](#2-hedge-policy-speculative-concurrency-for-long-tail-latency-elimination)
  - [2.1 Theoretical Foundation: "The Tail at Scale" (Dean & Barroso)](#21-theoretical-foundation-the-tail-at-scale-dean--barroso)
  - [2.2 Sequential Retries vs. Speculative Hedging](#22-sequential-retries-vs-speculative-hedging)
  - [2.3 Dynamic Delay Tuning via Live $p_{95}/p_{99}$ Quantiles](#23-dynamic-delay-tuning-via-live-p_95p_99-quantiles)
  - [2.4 Cancellation Semantics & Global Execution Budgets](#24-cancellation-semantics--global-execution-budgets)
  - [2.5 Idiomatic Go Implementation: Resilient Hedge Pipeline](#25-idiomatic-go-implementation-resilient-hedge-pipeline)
- [3. Adaptive Limiter: Autonomous Concurrency Control via TCP Vegas & Gradient Analysis](#3-adaptive-limiter-autonomous-concurrency-control-via-tcp-vegas--gradient-analysis)
  - [3.1 Moving Beyond Static Bulkheads (Netflix Concurrency-Limits & Uber Cinnamon)](#31-moving-beyond-static-bulkheads-netflix-concurrency-limits--uber-cinnamon)
  - [3.2 The Modified TCP Vegas Thresholding Algorithm](#32-the-modified-tcp-vegas-thresholding-algorithm)
  - [3.3 Throughput-to-Inflight Correlation: Detecting Saturation Before Timeouts](#33-throughput-to-inflight-correlation-detecting-saturation-before-timeouts)
  - [3.4 Proportional Queueing & Gradual Rejection Scaling](#34-proportional-queueing--gradual-rejection-scaling)
  - [3.5 Idiomatic Go Implementation: Adaptive Limiter Configuration](#35-idiomatic-go-implementation-adaptive-limiter-configuration)
- [4. Adaptive Throttler: Probabilistic Client-Side Load Shedding (Google SRE)](#4-adaptive-throttler-probabilistic-client-side-load-shedding-google-sre)
  - [4.1 Theoretical Foundation: The Google SRE Client-Side Throttling Formula](#41-theoretical-foundation-the-google-sre-client-side-throttling-formula)
  - [4.2 Circuit Breakers vs. Adaptive Throttlers: All-or-Nothing vs. Smooth Proportional Shedding](#42-circuit-breakers-vs-adaptive-throttlers-all-or-nothing-vs-smooth-proportional-shedding)
  - [4.3 Max Rejection Rate Clamping & The Probing Recovery Guarantee](#43-max-rejection-rate-clamping--the-probing-recovery-guarantee)
  - [4.4 Idiomatic Go Implementation: Adaptive Throttler Policy](#44-idiomatic-go-implementation-adaptive-throttler-policy)
- [5. Rate Limiter: Smooth (Leaky Bucket) vs. Bursty (Fixed Window)](#5-rate-limiter-smooth-leaky-bucket-vs-bursty-fixed-window)
  - [5.1 Smooth Rate Limiting: Spreading Load Evenly Over Time](#51-smooth-rate-limiting-spreading-load-evenly-over-time)
  - [5.2 Bursty Rate Limiting: Permitting Controlled Spikes](#52-bursty-rate-limiting-permitting-controlled-spikes)
  - [5.3 Asynchronous Permit Reservation (`ReservePermit`) & Standalone Schedulers](#53-asynchronous-permit-reservation-reservepermit--standalone-schedulers)
  - [5.4 Idiomatic Go Implementation: Smooth & Bursty Rate Limiters](#54-idiomatic-go-implementation-smooth--bursty-rate-limiters)
- [6. Cache Policy: In-Pipeline Result Deduplication & Conditional Caching](#6-cache-policy-in-pipeline-result-deduplication--conditional-caching)
  - [6.1 Cache as a First-Class Resilience Policy](#61-cache-as-a-first-class-resilience-policy)
  - [6.2 Context-Driven Cache Keys (`ContextWithCacheKey`)](#62-context-driven-cache-keys-contextwithcachekey)
  - [6.3 Conditional Caching (`CacheIf`) & Telemetry Listeners](#63-conditional-caching-cacheif--telemetry-listeners)
  - [6.4 Idiomatic Go Implementation: Cache Policy Integration](#64-idiomatic-go-implementation-cache-policy-integration)
- [7. Master Policy Composition Matrix: The Extended Execution Onion](#7-master-policy-composition-matrix-the-extended-execution-onion)

---

## 1. The Complete Failsafe-Go Policy Taxonomy

Resilience policies in `failsafe-go` are divided into three core functional categories:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         FAILSAFE-GO POLICY TAXONOMY                         │
├──────────────────────────────┬──────────────────────────────┬───────────────┤
│    1. FAILURE HANDLING       │      2. LOAD LIMITING        │ 3. TIME / TAIL│
├──────────────────────────────┼──────────────────────────────┼───────────────┤
│ • Retry Policy               │ • Circuit Breaker            │ • Timeout     │
│ • Fallback Policy            │ • Adaptive Limiter           │ • Hedge Policy│
│                              │ • Adaptive Throttler         │               │
│                              │ • Rate Limiter (Smooth/Burst)│               │
│                              │ • Bulkhead                   │               │
│                              │ • Cache Policy               │               │
└──────────────────────────────┴──────────────────────────────┴───────────────┘
```

---

## 2. Hedge Policy: Speculative Concurrency for Long-Tail Latency Elimination

### 2.1 Theoretical Foundation: "The Tail at Scale" (Dean & Barroso)
In large distributed systems, high $p_{99}$ latency is rarely caused by heavy computational load; it is driven by background operating system jitter, garbage collection pauses, disk head contention, and packet loss.

Jeffrey Dean and Luiz André Barroso (Google) demonstrated that if a service calls 100 independent sub-services, and each sub-service has a $1\%$ probability of a $1\text{s}$ pause ($p_{99} = 1\text{s}$), the probability that the user request experiences a $1\text{s}$ delay is:
$$P(\text{User Delay} \ge 1\text{s}) = 1 - (1 - 0.01)^{100} = \mathbf{63.4\%}$$

**The Hedging Solution**: Instead of waiting for a slow request to time out and fail, spawn a concurrent speculative second request (a **hedge**) after a small delay (e.g. $p_{95}$ latency). As soon as the first successful response arrives, cancel all outstanding attempts.

```
Request 1 (Primary) ──[ Stalled in GC Pause... ]────────────────────[ CANCELED ]
                           │
                 Wait: Dynamic Delay (e.g. p95 = 45ms)
                           │
                           ▼
Request 2 (Hedge 1) ───────────[ Fast Execution: 12ms ]──> [ 200 OK SUCCESS ]
```

### 2.2 Sequential Retries vs. Speculative Hedging

| Dimension | Sequential Retry Policy | Speculative Hedge Policy |
| :--- | :--- | :--- |
| **Execution Trigger** | Attempt 1 must fail or time out completely before Attempt 2 starts | Attempt 2 starts after a delay **while Attempt 1 is still running** |
| **Concurrency** | Strict $N=1$ concurrency per request | Multiple ($N \ge 2$) executions run concurrently |
| **Tail Latency Impact**| Additive: $T_{total} = \text{Timeout}_1 + \text{Delay} + \text{Duration}_2$ | Sub-additive: $T_{total} = \min(\text{Duration}_1, \text{Delay} + \text{Duration}_2)$ |
| **Network Cost** | Low overhead (only runs on failure) | Minor additional traffic ($1–5\%$ additional requests) |

### 2.3 Dynamic Delay Tuning via Live $p_{95}/p_{99}$ Quantiles
Setting a static hedging delay (e.g., $100\text{ms}$) fails if system load fluctuates. If baseline latency drops to $10\text{ms}$, a $100\text{ms}$ delay never hedges. If baseline latency rises to $120\text{ms}$, a $100\text{ms}$ delay hedges $100\%$ of requests, doubling downstream traffic.

**Best Practice**: Bind `WithDelayFunc` to a live rolling $p_{95}$ latency estimator.

### 2.4 Cancellation Semantics & Global Execution Budgets
- **`CancelOnResult` / `CancelOnError`**: By default, any completed response cancels outstanding hedges.
- **Hedging Execution Budgets**: To prevent hedging from overloading downstream servers during a major network partition, assign an execution budget that caps max concurrent hedges across the entire process to $\le 10\%$ of total traffic.

### 2.5 Idiomatic Go Implementation: Resilient Hedge Pipeline

```go
package hedge_demo

import (
	"context"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/hedgepolicy"
)

func BuildResilientHedgeExecutor(p95Provider func() time.Duration) failsafe.Executor[[]byte] {
	hedge := hedgepolicy.BuilderWithDelayFunc[[]byte](func(exec failsafe.ExecutionAttempt[[]byte]) time.Duration {
		return p95Provider() // Dynamically hedge at current p95 latency
	}).
		WithMaxHedges(2).
		CancelOnResult(nil). // Cancel remaining hedges once any result arrives
		OnHedge(func(e failsafe.ExecutionEvent[[]byte]) {
			// Record telemetry metric for speculative hedge trigger
		}).
		Build()

	return failsafe.NewExecutor[[]byte](hedge)
}
```

---

## 3. Adaptive Limiter: Autonomous Concurrency Control via TCP Vegas & Gradient Analysis

### 3.1 Moving Beyond Static Bulkheads (Netflix Concurrency-Limits & Uber Cinnamon)
Static bulkheads require manual capacity guessing. If an engineer configures a bulkhead of `MaxConcurrency: 50`:
- If downstream dependencies speed up, the system artificially starves itself of throughput.
- If downstream dependencies slow down, a concurrency of 50 causes queuing, socket bloat, and memory exhaustion.

**Adaptive Limiters** continuously discover and adjust the optimal concurrency limit without manual intervention.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│               ADAPTIVE LIMITER CONCURRENCY REGULATION LOOP                  │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                     [ In-Flight Requests Processing ]
                                       │
             ┌─────────────────────────┴─────────────────────────┐
             ▼                                                   ▼
  [ 1. Execution Times ]                              [ 2. Throughput Correlation ]
  Recent Sample Quantile (p90)                        Are inflight reqs increasing
  compared against Weighted Moving                    while throughput is flat/falling?
  Average Baseline Window (RTT_base)                                 │
             │                                                       │
             └─────────────────────────┬─────────────────────────────┘
                                       │
                                       ▼
                       [ Modified TCP Vegas Gradient ]
                     Gradient = (RTT_base / RTT_recent)
                                       │
                     ┌─────────────────┴─────────────────┐
                     │                                   │
              Gradient < 1.0                      Gradient >= 1.0
          (Queuing / Overload Detected)       (Headroom Available)
                     │                                   │
                     ▼                                   ▼
             DECREASE LIMIT                       INCREASE LIMIT
```

### 3.2 The Modified TCP Vegas Thresholding Algorithm
The adaptive limiter measures round-trip latency ($RTT$) and maintains a long-term minimum baseline ($RTT_{base}$):

$$\text{Gradient} = \frac{RTT_{base}}{RTT_{recent}}$$
$$\text{EstimatedQueue} = \text{CurrentLimit} \times (1 - \text{Gradient})$$

- If $\text{EstimatedQueue} > \alpha$, the system is experiencing internal queue bloat $\implies$ **Reduce Limit**.
- If $\text{EstimatedQueue} < \beta$, the system has unused capacity $\implies$ **Increase Limit**.

### 3.3 Throughput-to-Inflight Correlation: Detecting Saturation Before Timeouts
Traditional circuit breakers require actual errors (500s or timeouts) to trigger. Adaptive limiters observe the **derivative of throughput with respect to concurrency**:
$$\frac{\partial \text{Throughput}}{\partial \text{Inflight}} \le 0$$
When adding more concurrent requests fails to produce higher completed requests per second, the service is saturated. The adaptive limiter caps concurrency **before a single timeout occurs**.

### 3.4 Proportional Queueing & Gradual Rejection Scaling
Instead of hard drop-fast rejections, adaptive limiters support proportional queueing:
- Current limit: $L = 20$.
- Initial Rejection Factor: $2 \implies$ Up to $40$ requests can queue before rejections start.
- Max Rejection Factor: $3 \implies$ At $60$ queued requests, $100\%$ of new traffic is rejected with `adaptivelimiter.ErrExceeded`.

### 3.5 Idiomatic Go Implementation: Adaptive Limiter Configuration

```go
package limiter_demo

import (
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/adaptivelimiter"
)

func BuildAdaptiveConcurrencyLimiter() failsafe.Executor[any] {
	limiter := adaptivelimiter.Builder[any]().
		WithLimits(5, 200, 25). // Min: 5, Max: 200, Initial: 25
		WithRecentWindow(500*time.Millisecond, 10*time.Second, 40).
		WithRecentQuantile(0.90). // Compare p90 recent latency to baseline
		WithBaselineWindow(15).
		WithQueueing(2, 3). // Proportional queueing (2x initial, 3x max)
		OnLimitChanged(func(e adaptivelimiter.LimitChangedEvent) {
			// Export gauge to Prometheus
		}).
		Build()

	return failsafe.NewExecutor[any](limiter)
}
```

---

## 4. Adaptive Throttler: Probabilistic Client-Side Load Shedding (Google SRE)

### 4.1 Theoretical Foundation: The Google SRE Client-Side Throttling Formula
Chapter 21 of the *Google SRE Book* defines a mathematical client-side load shedding formula to protect overloaded backends:

$$P(\text{Drop}) = \max\left(0, \frac{\text{Requests} - K \cdot \text{Accepts}}{\text{Requests} + 1}\right)$$

Where:
- $\text{Requests}$ = Total attempts sent by client over rolling window.
- $\text{Accepts}$ = Total successful responses accepted by backend.
- $K$ = Multiplier (typically $K = 2.0$, allowing the client to send 2x successful volume).

When the failure rate rises, the probability of locally dropping a request increases smoothly and continuously.

```
100% ┌────────────────────────────────────────────────────────────────────────┐
     │                                                     / Max Rejection 90%│
 D   │                                                    /                   │
 R   │                                                   /                    │
 O P │                                                  /                     │
 P % │                                                 /                      │
     │                                                /                       │
  0% └───────────────────────────────────────────────┴────────────────────────┘
     0% Failures                   Threshold (e.g. 10%)           100% Failures
```

### 4.2 Circuit Breakers vs. Adaptive Throttlers: All-or-Nothing vs. Smooth Proportional Shedding

| Dimension | Circuit Breaker | Adaptive Throttler |
| :--- | :--- | :--- |
| **Shedding Model** | **Binary (All-or-Nothing)**: Passes $100\%$ or drops $100\%$ | **Probabilistic (Proportional)**: Drops $10\% \dots 90\%$ based on error severity |
| **Downstream Impact** | Sharp cliff; sudden traffic drop followed by sudden probe burst | Smooth reduction; downscales traffic precisely to backend capacity |
| **Recovery Discovery** | Blind trial probe during Half-Open | Continuous self-probing traffic ($1 - \text{MaxRejectionRate}$) |

### 4.3 Max Rejection Rate Clamping & The Probing Recovery Guarantee
An adaptive throttler must **never** reject $100\%$ of requests ($\text{MaxRejectionRate} \le 0.90$):
- If a throttler rejected $100\%$ of traffic, $\text{Accepts}$ would remain $0$ forever, preventing the client from ever discovering that the downstream backend has recovered.
- Clamping rejection to $90\%$ ensures $10\%$ of traffic continuously probes the backend, automatically resetting the throttler as successes return.

### 4.4 Idiomatic Go Implementation: Adaptive Throttler Policy

```go
package throttler_demo

import (
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/adaptivethrottler"
)

func BuildAdaptiveThrottler() failsafe.Executor[any] {
	throttler := adaptivethrottler.Builder[any]().
		HandleErrors(ErrGateway503, ErrConnectionTimeout).
		WithFailureRateThreshold(0.10, 10, time.Minute). // Trip threshold: 10% errors
		WithMaxRejectionRate(0.85).                       // Clamp max drop to 85%
		Build()

	return failsafe.NewExecutor[any](throttler)
}
```

---

## 5. Rate Limiter: Smooth (Leaky Bucket) vs. Bursty (Fixed Window)

### 5.1 Smooth Rate Limiting: Spreading Load Evenly Over Time
A **Smooth Rate Limiter** uses a leaky bucket algorithm to enforce an exact maximum inter-arrival rate:
- Setting `100 executions per second` forces executions to be spaced by at least $\Delta t = \frac{1\text{s}}{100} = \mathbf{10\text{ms}}$.
- Eliminates traffic bursts, preventing downstream queue spikes.

### 5.2 Bursty Rate Limiting: Permitting Controlled Spikes
A **Bursty Rate Limiter** uses a fixed window / token bucket model:
- Setting `10 executions per second` permits all 10 requests to fire simultaneously at $t=0.00\text{s}$.
- Requests 11 through $N$ are rejected or queued until $t=1.00\text{s}$.

### 5.3 Asynchronous Permit Reservation (`ReservePermit`) & Standalone Schedulers
Beyond wrapping synchronous calls, `failsafe-go` rate limiters support non-blocking permit reservation:
```go
// Standalone reservation for job schedulers
waitTime := rateLimiter.ReservePermit()
time.AfterFunc(waitTime, func() {
    go dispatchAsyncJob()
})
```

### 5.4 Idiomatic Go Implementation: Smooth & Bursty Rate Limiters

```go
package ratelimit_demo

import (
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/ratelimiter"
)

// Smooth: 100 RPS evenly spaced (1 per 10ms)
func NewSmoothLimiter() failsafe.Executor[any] {
	limiter := ratelimiter.Builder[any](ratelimiter.NewSmooth(100, time.Second)).
		WithMaxWaitTime(50 * time.Millisecond). // Allow queuing up to 50ms
		Build()
	return failsafe.NewExecutor[any](limiter)
}

// Bursty: Up to 50 RPS in bursts
func NewBurstyLimiter() failsafe.Executor[any] {
	limiter := ratelimiter.Builder[any](ratelimiter.NewBursty(50, time.Second)).
		Build()
	return failsafe.NewExecutor[any](limiter)
}
```

---

## 6. Cache Policy: In-Pipeline Result Deduplication & Conditional Caching

### 6.1 Cache as a First-Class Resilience Policy
In `failsafe-go`, caching is composed directly into the execution onion via `CachePolicy`. If a valid entry exists for the cache key, the executor returns the cached value in $< 1\mu\text{s}$ without calling downstream functions.

### 6.2 Context-Driven Cache Keys (`ContextWithCacheKey`)
Different requests require different cache keys (e.g. `order_id` or `customer_id`):

```go
ctx := cachepolicy.ContextWithCacheKey(context.Background(), fmt.Sprintf("user_tier_%s", customerID))
result, err := executor.WithContext(ctx).Get(fetchUserTier)
```

### 6.3 Conditional Caching (`CacheIf`) & Telemetry Listeners
Cache only successful HTTP 200 responses, ignoring 500s or empty arrays:
```go
builder.CacheIf(func(resp *http.Response, err error) bool {
    return err == nil && resp != nil && resp.StatusCode == 200
})
```

### 6.4 Idiomatic Go Implementation: Cache Policy Integration

```go
package cache_demo

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/cachepolicy"
)

type ThreadSafeMemoryCache struct {
	mu    sync.RWMutex
	store map[string]any
}

func (c *ThreadSafeMemoryCache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.store[key]
	return val, ok
}

func (c *ThreadSafeMemoryCache) Set(key string, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = val
}

func BuildCacheExecutor(cache *ThreadSafeMemoryCache) failsafe.Executor[*http.Response] {
	cp := cachepolicy.Builder[*http.Response](cache).
		WithKey("default_catalog").
		CacheIf(func(resp *http.Response, err error) bool {
			return err == nil && resp != nil && resp.StatusCode == 200
		}).
		OnCacheHit(func(e failsafe.ExecutionDoneEvent[*http.Response]) {
			// Prometheus cache_hit_total.Inc()
		}).
		OnCacheMiss(func(e failsafe.ExecutionEvent[*http.Response]) {
			// Prometheus cache_miss_total.Inc()
		}).
		Build()

	return failsafe.NewExecutor[*http.Response](cp)
}
```

---

## 7. Master Policy Composition Matrix: The Extended Execution Onion

When combining multiple failure-handling, load-limiting, and tail-mitigating policies, they wrap from **Outer to Inner**:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                 THE EXTENDED 8-LAYER FAILSAFE POLICY ONION                  │
├─────────────────────────────────────────────────────────────────────────────┤
│ 1. Fallback Policy          (Catches all terminal errors / degraded return) │
│   │                                                                         │
│ 2. Cache Policy             (Returns cached value in <1µs on hit)           │
│   │                                                                         │
│ 3. Overall Operation Timeout(Bounds cumulative execution SLA)               │
│   │                                                                         │
│ 4. Retry OR Hedge Policy    (Retries sequentially OR hedges speculatively)  │
│   │                                                                         │
│ 5. Circuit Breaker /        (Fast-fails <1ms OR probabilistically sheds     │
│    Adaptive Throttler        traffic during downstream failure spikes)      │
│   │                                                                         │
│ 6. Adaptive Limiter /       (Regulates concurrency via TCP Vegas / rates)   │
│    Rate Limiter                                                             │
│   │                                                                         │
│ 7. Bulkhead                 (Isolates process thread / socket quotas)       │
│   │                                                                         │
│ 8. Per-Attempt Timeout      (Strict deadline on single socket invocation)   │
│   │                                                                         │
│   ▼                                                                         │
│ Target Function             (Executes downstream network / database I/O)    │
└─────────────────────────────────────────────────────────────────────────────┘
```

Composition Code:
```go
executor := failsafe.With(
    fallbackPolicy,
    cachePolicy,
    overallTimeout,
    retryOrHedgePolicy,
    circuitBreakerOrThrottler,
    adaptiveOrRateLimiter,
    bulkheadPolicy,
    attemptTimeout,
)
```
