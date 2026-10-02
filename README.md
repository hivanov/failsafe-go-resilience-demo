# failsafe-go Architecture & Live Demo: SLA-Driven Fault Tolerance

> **Engineering for Reality: SLA-Driven Fault Tolerance in a Single Go Process**

This repository contains a production-grade demonstration, parameterized test suite, and live PostgreSQL Testcontainers suite showcasing resilience patterns using [`failsafe-go`](https://github.com/failsafe-go/failsafe-go) (official documentation at [failsafe-go.dev](https://failsafe-go.dev)).

---

## Table of Contents

- [1. Architectural Case Study: "Checkout Orchestrator"](#1-architectural-case-study-checkout-orchestrator)
  - [Dependency Classification & Time Budget Allocation](#dependency-classification--time-budget-allocation)
- [2. Policy Composition (The Execution Onion)](#2-policy-composition-the-execution-onion)
- [3. Package Structure & Resilient Decorator Architecture](#3-package-structure--resilient-decorator-architecture)
  - [Key Source References](#key-source-references)
- [4. Architectural Deep Dives & Further Reading](#4-architectural-deep-dives--further-reading)
- [5. Running the Interactive Demo](#5-running-the-interactive-demo)
  - [Live Scenarios Demonstrated](#live-scenarios-demonstrated)
- [6. Running the Parameterized & Testcontainers Test Suite](#6-running-the-parameterized--testcontainers-test-suite)

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
    ├── README.md                    # Deep-dive documentation index & TOC
    ├── design_process_and_modularity.md # 80/20 rule, 7-phase design cycle & resource governance
    ├── anti_patterns.md             # Detailed guide on 20 resilience anti-patterns
    ├── operational_resilience_and_support_processes.md # Support operations, alerts, runbooks & GameDays
    ├── acid_monolith_architecture.md# Single ACID database architecture & trade-offs
    ├── distributed_transactions_sagas.md # Sagas, 2PC, rollbacks, and outbox patterns
    ├── context_cancellation_best_practices.md # Go context & socket leak rules
    ├── product_owner_interview_and_caching_fallbacks.md # 4 PO questions & multi-tier caching fallbacks (CDN, Redis, Memory, Solace)
    ├── workflow_engines_vs_code_orchestration.md # Low-code workflow engines vs in-process Go orchestration
    └── further_reading_and_bibliography.md # Master bibliography, Jesse Robbins talks, testing
```

### Key Source References:
- **Resilient Decorators:** [`pkg/policies/payment_policy.go`](pkg/policies/payment_policy.go), [`pkg/policies/inventory_policy.go`](pkg/policies/inventory_policy.go), [`pkg/policies/fraud_policy.go`](pkg/policies/fraud_policy.go), [`pkg/policies/loyalty_policy.go`](pkg/policies/loyalty_policy.go).
- **Domain Orchestrator:** [`pkg/checkout/orchestrator.go`](pkg/checkout/orchestrator.go) (cleanly delegates resilience to injected decorators).
- **Strongly-Typed Domain Models & Enums:** [`pkg/checkout/models.go`](pkg/checkout/models.go) (`OrderStatus`, `PaymentStatus`, `RiskDecision`).
- **Composition Root:** [`cmd/demo/main.go`](cmd/demo/main.go).

---

## 4. Architectural Deep Dives & Further Reading

All comprehensive deep-dive guides, distributed transaction strategies, database architecture trade-offs, resilience engineering methodologies, and curated literature references are indexed in **[`doc/README.md`](doc/README.md)**:

- 📖 **[Workflow Engines vs. In-Process Go Orchestration: Trade-offs & Unit Economics](doc/workflow_engines_vs_code_orchestration.md)** — The fallacy of "No-Code Resilience": Why visual workflow engines (Google Cloud Workflows, Microsoft PowerAutomate, AWS Step Functions, Azure Logic Apps, Camunda) do NOT relieve developers from policy design; Comprehensive trade-off matrix: Sub-microsecond latency vs 200ms step overhead, near-zero cost vs $25/M transition billing, compile-time safety vs YAML/JSON DSL hell, local testing via Testcontainers vs cloud sandboxes; In-flight workflow version drift and schema migration challenges; Two-tier hybrid architecture (Temporal/Step Functions coarse-grained durable lifecycle + `failsafe-go` fine-grained synchronous hot-path execution).
- 📖 **[Stakeholder Interview Framework & Caching Fallback Architectures in Go](doc/product_owner_interview_and_caching_fallbacks.md)** — The 4 mandatory Product Owner interview questions (Degraded behavior, Error classification, SLA & user patience budgets, Financial cost & risk trade-offs); Translation matrix from business answers to `failsafe-go` policy composition; Multi-tier caching fallback architectures: Tier 0 Edge CDN whole request-response caching (RFC 5861 `stale-if-error`, `stale-while-revalidate`, ETag conditional requests), Tier 1 Remote Redis/MongoDB caches with singleflight stampede suppression and cache circuit breakers, Tier 2 In-memory local caches (`sync.RWMutex` with bounded TTL), and Tier 3 Event-synchronized in-memory cache over Solace PubSub+ and Kafka guaranteed messaging.
- 📖 **[Resilience Design Process & Modularity Principles in Go](doc/design_process_and_modularity.md)** — The 80/20 Pareto rule in resilience engineering; The 7-phase iterative design cycle; Resource governance deep-dive (Kubernetes CFS throttling, `GOMEMLIMIT`, socket pools, GPU VRAM, swap thrashing); Interface segregation and decorator modularity in Go.
- 📖 **[Architectural Anti-Patterns in Resilient System Design](doc/anti_patterns.md)** — Breakdown of 20 fatal anti-patterns (unbounded retries, missing jitter, non-idempotent retries, locks & deadlocks, inadequate testing, five-nines fantasy, hunch-based timeouts, context disconnection, cascading shared circuit breakers, CFS throttling, OOMKills, socket leaks, goroutine explosions).
- 📖 **[Coupling Software Resilience with Operational Support Processes](doc/operational_resilience_and_support_processes.md)** — Safe-to-Fail mindset; On-call ownership; Symptom-based alert hygiene; Progressive remediation (Manual Runbooks $\rightarrow$ GameDays $\rightarrow$ Automated Self-Healing).
- 📖 **[Single-Database ACID Architecture vs. Distributed Microservices](doc/acid_monolith_architecture.md)** — Why single-database relational architectures win at moderate scale (< 500k DAU) with instant zero-code rollbacks (`ROLLBACK`) and kernel-level locking.
- 📖 **[Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](doc/distributed_transactions_sagas.md)** — Sagas vs. 2PC, Forward ($T_i$) vs. Compensating ($C_i$) actions, Transactional Outbox pattern, and distributed rollback rules.
- 📖 **[Context Propagation & Socket Leak Prevention in Go](doc/context_cancellation_best_practices.md)** — How `exec.Context()` bridges `failsafe-go` policies to network sockets and prevents silent server resource leaks.
- 📖 **[Further Reading, Bibliography & Historical Media](doc/further_reading_and_bibliography.md)** — Master bibliography (Nygard, Kleppmann, Tanenbaum, TailoredRead, Richardson); Historical conference talks (Jesse Robbins: *Operations at Web Scale*, *GameDay: Master of Disaster*; John Allspaw); Repeatable testing methodologies (Testcontainers, virtual clocks `clockwork`, deterministic PRNG jitter seeding).

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

## 6. Running the Parameterized & Testcontainers Test Suite

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
