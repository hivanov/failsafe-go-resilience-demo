# Common Anti-Patterns in Resilient System Design

Building resilient software in Go requires understanding not just how to configure policies, but how flawed architectural assumptions and misconfigurations actively destabilize distributed systems. Below is an exhaustive breakdown of the most critical resilience anti-patterns encountered in production services, including infrastructure resource governance, Kubernetes limits, socket handling, and hardware exhaustion.

---

## Table of Contents

- [1. Unbounded Retries (The Infinite Retry Loop)](#1-unbounded-retries-the-infinite-retry-loop)
- [2. Retries Without Jitter (The Thundering Herd / Synchronized Stampede)](#2-retries-without-jitter-the-thundering-herd--synchronized-stampede)
- [3. Retrying Non-Idempotent Operations (The Double-Charge Disaster)](#3-retrying-non-idempotent-operations-the-double-charge-disaster)
- [4. Distributed & In-Process Locking Pitfalls (+ Deadlocks)](#4-distributed--in-process-locking-pitfalls--deadlocks)
- [5. Inadequate Testing: Mock-Driven Illusion vs. Real Infrastructure](#5-inadequate-testing-mock-driven-illusion-vs-real-infrastructure)
- [6. Lack of Real-World Observations (Designing in a Telemetry Vacuum)](#6-lack-of-real-world-observations-designing-in-a-telemetry-vacuum)
- [7. Lack of Business Awareness (Building for Incorrect Scenarios & Load Profiles)](#7-lack-of-business-awareness-building-for-incorrect-scenarios--load-profiles)
- [8. Over-Engineering SLA Requirements (The "Five-Nines" Fantasy)](#8-over-engineering-sla-requirements-the-five-nines-fantasy)
- [9. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)](#9-lack-of-alternative-strategies-binary-success-or-fail-thinking)
- [10. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps](#10-ignoring-the-critical-path--over-optimizing-non-critical-steps)
- [11. Basing Policies on "Hunches" Instead of Empirical SLAs](#11-basing-policies-on-hunches-instead-of-empirical-slas)
- [12. Context Disconnection & Socket Leaking](#12-context-disconnection--socket-leaking)
- [13. Blind / Catch-All Error Retries (Retrying Deterministic Failures)](#13-blind--catch-all-error-retries-retrying-deterministic-failures)
- [14. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)](#14-cascading-circuit-breaker-trips-shared-breakers-across-disparate-endpoints)
- [15. Kubernetes CPU Limit: CFS Quota Throttling](#15-kubernetes-cpu-limit-cfs-quota-throttling)
- [16. Missing GOMEMLIMIT: OOMKilled Containers (Exit Code 137)](#16-missing-gomemlimit-oomkilled-containers-exit-code-137)
- [17. Ephemeral Port & Socket Leaking (Unpooled HTTP Clients & Unclosed Bodies)](#17-ephemeral-port--socket-leaking-unpooled-http-clients--unclosed-bodies)
- [18. Goroutine Explosion & Unbounded Concurrency (The Missing Bulkhead)](#18-goroutine-explosion--unbounded-concurrency-the-missing-bulkhead)
- [19. GPU Resource Exhaustion: VRAM Saturation & Host PCIe Bottlenecks](#19-gpu-resource-exhaustion-vram-saturation--host-pcie-bottlenecks)
- [20. Swap Thrashing & Memory Paging GC Latency](#20-swap-thrashing--memory-paging-gc-latency)

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

## 8. Over-Engineering SLA Requirements (The "Five-Nines" Fantasy)
- **The Anti-Pattern:** Demanding $99.999\%$ ("five nines" = $\le 5.26\text{ minutes}$ downtime/year) or $99.9999\%$ availability for a monthly batch billing process, or demanding five-nines on top of underlying cloud components (e.g. AWS ALB + RDS) with a composite infrastructure SLA of $99.95\%$.
- **The Mathematical Reality of Serial Availability:** A system's composite uptime is bounded by the product of its serial dependencies:
  $$A_{\text{total}} = A_{\text{cloud}} \times A_{\text{database}} \times A_{\text{gateway}} = 0.9995 \times 0.9995 \times 0.999 = 99.8\%$$
  Attempting to achieve 99.999% on top of 99.9% dependencies requires multi-region, multi-cloud active-active infrastructure, consensus quorums, and dedicated SRE teams—multiplying development and operational costs by $20\times$ to $50\times$ for zero tangible business value.
- **The Failure Mode:** Exhausting engineering budgets, adding extreme operational fragility, and creating burnout chasing uptime metrics that the business model does not require.
- **Remediation:** Establish pragmatic Service Level Objectives (SLOs) tied to real business impact. Use error budgets to balance feature velocity with operational stability.
- **Code Reference:** See [`doc/operational_resilience_and_support_processes.md`](./operational_resilience_and_support_processes.md).

---

## 9. Lack of Alternative Strategies (Binary Success-or-Fail Thinking)
- **The Anti-Pattern:** Treating every dependency as binary: either it succeeds with 100% fidelity or the entire user request throws a 500 Internal Server Error.
- **The Failure Mode:** When a soft dependency (like an ML fraud scoring service, recommendation widget, or loyalty reward ledger) experiences a slow path, the entire critical checkout pipeline fails, leading to lost revenue and frustrated users.
- **Remediation:** Classify dependencies by business criticality. Equip all soft and semi-critical dependencies with **Fallback Policies**:
  - *Heuristic Fallback:* Degrade to fast rule-based risk checks when ML inference times out.
  - *Asynchronous Queue Fallback:* Route orders with unavailable payment gateways to a manual review queue (`REVIEW_PENDING`) rather than immediately aborting the customer session.
  - *Stale Cache Fallback:* Serve cached product catalog prices or static currency exchange rates.
- **Code Reference:** See [`pkg/policies/fraud_policy.go`](../pkg/policies/fraud_policy.go) (`ResilientFraudService`) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`ResilientPaymentGateway`).

---

## 10. Ignoring the Critical Path & Over-Optimizing Non-Critical Steps
- **The Anti-Pattern:** Spending engineering time tuning retries on non-critical analytics or email dispatchers while leaving the core database lock and payment gateway unbudgeted.
- **The Failure Mode:** The user-facing latency budget is consumed by the wrong activities. Slow critical operations breach SLA while background work monopolizes worker resources.
- **Remediation:** Map the end-to-end request timeline. Identify the sequential **Critical Path** (e.g. Ingress $\rightarrow$ Fraud $\rightarrow$ Inventory DB Lock $\rightarrow$ Payment Charge). Allocate explicit time slices to each critical step. Move all non-critical work (e.g. Loyalty point calculation, email notification) off the critical path into asynchronous background goroutines bounded by separate contexts.
- **Code Reference:** See [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go) (sequential critical path followed by asynchronous `loyaltySvc.AccruePoints`).

---

## 11. Basing Policies on "Hunches" Instead of Empirical SLAs
- **The Anti-Pattern:** Picking arbitrary timeout and retry numbers (e.g. "let's set a 5-second timeout and 5 retries") without analyzing real telemetry or upstream SLA contracts.
- **The Failure Mode:**
  - *Timeout < P95 Latency:* If a downstream database query has a legitimate P95 latency of 300ms, setting a hunch-based timeout of 200ms causes a **self-inflicted outage** where 5% of healthy requests are aggressively aborted.
  - *Timeout > Client SLA:* Setting a 3-second timeout when the upstream API gateway SLA is 800ms means the upstream client disconnects long before the service gives up, wasting CPU and database capacity.
- **Remediation:** Instrument dependencies with OpenTelemetry and Prometheus histograms. Set timeout thresholds based on empirical data ($> \text{P99} + \text{buffer}$ for normal operations, strictly below total SLA budget).
- **Code Reference:** See [`pkg/checkout/telemetry.go`](../pkg/checkout/telemetry.go) and [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go).

---

## 12. Context Disconnection & Socket Leaking
- **The Anti-Pattern:** Declaring failsafe timeout policies but failing to pass `exec.Context()` to the underlying `http.Client` or `database/sql` driver.
- **The Failure Mode:** When the failsafe timeout fires, the orchestrator proceeds, but the background goroutine and network socket continue running in the background until the OS TCP timeout (often 2 minutes). Under high concurrency, connection pools and file descriptors become saturated, leading to **silent server death**.
- **Remediation:** Always pass `exec.Context()` down the entire call stack and use `http.NewRequestWithContext` or `QueryContext`/`ExecContext`.
- **Code Reference:** See [`doc/context_cancellation_best_practices.md`](./context_cancellation_best_practices.md) and [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go).

---

## 13. Blind / Catch-All Error Retries (Retrying Deterministic Failures)
- **The Anti-Pattern:** Retrying on *any* error (`HandleErrors(err)` or catching all HTTP non-200 responses).
- **The Failure Mode:** Retrying deterministic 4xx client errors (e.g., `400 Bad Request`, `401 Unauthorized`, `404 Not Found`, `422 Unprocessable Entity`, invalid credit card number). These errors will **never** succeed on retry; retrying them only wastes CPU, consumes bandwidth, and slows down the client response.
- **Remediation:** Only retry **transient, recoverable errors** (e.g. `503 Service Unavailable`, `429 Too Many Requests`, temporary network drops, connection resets).
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) (`HandleErrors(ErrTransientNetwork, ErrGatewayUnavailable, ErrRateLimited)`).

---

## 14. Cascading Circuit Breaker Trips (Shared Breakers Across Disparate Endpoints)
- **The Anti-Pattern:** Using a single global circuit breaker instance to protect calls to multiple distinct external APIs or database tables.
- **The Failure Mode:** If an optional reporting endpoint goes down, the shared circuit breaker trips to `OPEN`, inadvertently taking down the critical payment processing pipeline with it.
- **Remediation:** Isolate circuit breakers per failure domain, per endpoint, or per microservice interface.
- **Code Reference:** See [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go) where the circuit breaker is strictly bound to `PaymentGateway`.

---

## 15. Kubernetes CPU Limit: CFS Quota Throttling
- **The Anti-Pattern:** Setting aggressive, tight CPU limits in Kubernetes manifests (e.g. `resources.limits.cpu: "500m"`) while running multithreaded Go applications with multiple goroutines.
- **The Mechanics of the Failure Mode:** 
  - Kubernetes enforces CPU limits using the Linux kernel Completely Fair Scheduler (CFS) quota mechanism over a default $100\text{ms}$ quota period (`cpu.cfs_period_us = 100000`).
  - If a container is assigned `500m` CPU ($0.5$ cores), it is allocated $50\text{ms}$ of total CPU time per $100\text{ms}$ window.
  - Go’s runtime (`GOMAXPROCS`) defaults to the **number of logical CPU cores on the host node** (e.g. 64 or 128 cores on a modern cloud VM), *not* the container's fractional CPU limit!
  - When a burst of 10 goroutines wake up simultaneously, they run across 10 OS threads, consuming the container's entire $50\text{ms}$ quota in just $5\text{ms}$ of wall-clock time.
  - The Linux kernel instantly **freezes/throttles the entire container process** for the remaining $95\text{ms}$ of the period.
- **The Failure Mode:** Severe, inexplicable latency spikes ($100\text{ms}\text{--}500\text{ms}$) on P99 response times without high average CPU utilization. Goroutines miss SLA timeouts despite the node being mostly idle.
- **Remediation:**
  1. Use `go.uber.org/automaxprocs` in `main.go` to automatically tune `GOMAXPROCS` to match the container's CPU quota.
  2. For latency-sensitive microservices, set `resources.requests.cpu` equal to `resources.limits.cpu` (Guaranteed QoS Class) or omit CPU limits entirely if cluster node governance permits.
  3. Monitor `container_cpu_cfs_throttled_periods_total` in Prometheus.

---

## 16. Missing GOMEMLIMIT: OOMKilled Containers (Exit Code 137)
- **The Anti-Pattern:** Setting Kubernetes memory limits (e.g. `resources.limits.memory: "1Gi"`) without configuring the Go runtime's memory limit (`GOMEMLIMIT`).
- **The Mechanics of the Failure Mode:** 
  - Prior to Go 1.19, the Go Garbage Collector (GC) was governed solely by `GOGC` (default `100`), which triggers a GC cycle only when heap memory grows by 100% since the last GC.
  - The Go runtime is **cgroup-blind by default**; it does not know the container has a 1GiB hard limit.
  - If live heap is $600\text{MB}$, `GOGC=100` targets the next collection at $1.2\text{GB}$.
  - When traffic spikes and memory reaches $1,024\text{MB}$, the Linux kernel cgroup OOM killer instantly terminates the container (`Exit Code 137 / OOMKilled`), dropping all active in-flight checkout requests without warning.
- **The Failure Mode:** Sudden, ungraceful pod restarts during traffic surges, dropping connections, aborting transactions, and creating restart cascading storms in Kubernetes.
- **Remediation:**
  - Always set `GOMEMLIMIT` in container environment variables to **80%–85% of the cgroup memory limit**:
    ```yaml
    env:
      - name: GOMEMLIMIT
        value: "850MiB" # For a 1GiB cgroup limit (leaves 15% buffer for binary, thread stacks, and OS)
      - name: GOGC
        value: "100"
    ```
  - This forces the Go GC to trigger aggressively as memory nears the cgroup ceiling, trading slight CPU cycles for zero OOMKill crashes.

---

## 17. Ephemeral Port & Socket Leaking (Unpooled HTTP Clients & Unclosed Bodies)
- **The Anti-Pattern:** 
  1. Instantiating a new `&http.Client{}` inside request handler functions instead of reusing a shared singleton transport.
  2. Forgetting to read and close `resp.Body` on outbound HTTP calls (`defer resp.Body.Close()`).
  3. Setting `Transport.DisableKeepAlives = true` or `MaxIdleConnsPerHost = 2` (default).
- **The Failure Mode:**
  - Every unpooled HTTP call opens a new TCP connection and ephemeral port (allocated from `net.ipv4.ip_local_port_range`, typically ~28,000 available ports).
  - When closed, TCP sockets linger in `TIME_WAIT` state for $60\text{ seconds}$ (`2 * MSL`).
  - Under modest load (e.g. 500 req/sec with retries), the container **exhausts all available ephemeral ports** in under a minute, throwing `dial tcp: dial: cannot assign requested address`.
  - Unclosed `resp.Body` prevents underlying TCP socket reuse, ballooning open file descriptors until hitting `ulimit -n` (throwing `socket: too many open files`).
- **Remediation:**
  - Maintain a shared, pooled `http.Client` with tuned connection pool limits:
    ```go
    var SharedTransport = &http.Transport{
        MaxIdleConns:        1000,
        MaxIdleConnsPerHost: 200, // Default is 2, causing massive socket churn!
        IdleConnTimeout:     90 * time.Second,
        DisableKeepAlives:   false,
    }
    ```
  - Always drain and close response bodies: `io.Copy(io.Discard, resp.Body); resp.Body.Close()`.

---

## 18. Goroutine Explosion & Unbounded Concurrency (The Missing Bulkhead)
- **The Anti-Pattern:** Spawning raw `go func()` goroutines for background tasks (e.g. loyalty accrual, analytics, retries) without concurrency bounding (bulkhead) or context propagation.
- **The Failure Mode:**
  - While an idle goroutine is lightweight (~2KB stack), active goroutines executing JSON parsing, DB queries, or allocating memory buffers consume tens or hundreds of kilobytes.
  - If a downstream service hangs, incoming requests spawn 50,000 concurrent goroutines in seconds.
  - 50,000 goroutines allocate gigabytes of heap, triggering GC CPU thrashing and container OOMKills.
- **Remediation:**
  - Wrap all concurrent operations in **Bounded Bulkheads** (using worker pools, buffered semaphore channels, or `failsafe-go/bulkhead`).
  - Always pass bounded contexts to background goroutines (`context.WithTimeout`).
- **Code Reference:** See [`pkg/policies/loyalty_policy.go`](../pkg/policies/loyalty_policy.go) for bounded background execution.

---

## 19. GPU Resource Exhaustion: VRAM Saturation & Host PCIe Bottlenecks
- **The Anti-Pattern:** Deploying Go inference workers interfacing with local GPU accelerators (e.g. PyTorch, ONNX Runtime, CUDA via cgo) without VRAM limits, memory pooling, or queue bulkheads.
- **The Failure Modes:**
  1. **CUDA Out of Memory (OOM):** Unlike CPU memory where allocations can fail gracefully or page, GPU VRAM allocations that exceed physical VRAM (e.g. 16GB or 24GB VRAM) throw fatal unrecoverable CUDA runtime exceptions that terminate the host Go process.
  2. **PCIe Bus Contention:** Ingesting large uncompressed payload tensors across the host CPU-GPU PCIe bus without pinned memory (`cudaHostAlloc`) or asynchronous streaming (`cudaStreamNonBlocking`), causing 80% of execution time to be spent waiting on memory transfer rather than tensor computation.
  3. **Lack of GPU Bulkheading:** Allowing 500 Go HTTP worker goroutines to invoke GPU inference concurrently. Because GPUs process workloads in synchronized warps/batches, massive unbatched concurrent GPU kernel launches thrash the GPU scheduler, driving P99 inference latency from $15\text{ms}$ to $3,000\text{ms}$.
- **Remediation:**
  - Place a strict **Bulkhead & Dynamic Batcher** in front of GPU kernels in Go: batch up to $N$ inference requests over a $5\text{ms}$ window.
  - Pre-allocate unified CUDA memory pools at service startup.
  - Bound concurrent kernel launches to match GPU hardware compute streams.

---

## 20. Swap Thrashing & Memory Paging GC Latency
- **The Anti-Pattern:** Enabling Linux swap on Kubernetes worker nodes (`NodeSwap`) without cgroup v2 memory-swap limits, or relying on swap as a "safety buffer" for memory-constrained Go microservices.
- **The Failure Mode:**
  - When physical RAM is exhausted, the Linux kernel begins swapping anonymous heap pages and Go goroutine stacks to disk (SSD/NVMe).
  - Go's garbage collector regularly traverses the entire heap space. When GC scanning touches swapped-out pages, it forces continuous random disk read I/O (page faults).
  - This causes **catastrophic swap thrashing**: GC pause times explode from $0.5\text{ms}$ to **15,000ms (15 seconds)**!
  - The Go binary appears completely frozen, failing liveness probes and breaching all upstream SLAs while CPU usage drops to 1% (stuck in `D` state / I/O wait).
- **Remediation:**
  - Disable swap on Kubernetes nodes (`swapoff -a` / `failSwapOn: true`), or configure cgroup v2 with strict memory-only limits (`memory.swap.max = 0`).
  - Rely on `GOMEMLIMIT` and pod autoscaling (HPA) rather than disk swap to absorb memory volatility.
