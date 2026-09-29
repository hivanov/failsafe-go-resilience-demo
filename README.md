# failsafe-go Architecture & Live Demo: SLA-Driven Fault Tolerance

> **Engineering for Reality: SLA-Driven Fault Tolerance in a Single Go Process**

This repository contains a production-grade demonstration and parameterized test suite showcasing resilience patterns using [`failsafe-go`](https://github.com/failsafe-go/failsafe-go).

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
        ├── [Hard Critical] Inventory DB Row Lock:     150ms max
        ├── [Semi-Critical] ML Fraud Evaluation:        100ms max (Strict fallback to heuristic rules)
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
│ 1. Fallback (Outer-most)                                  │
│    Catches gateway errors / timeout -> REVIEW_PENDING     │
│  ┌───────────────────────────────────────────────────────┐│
│  │ 2. Overall Timeout (400ms Time Budget Envelope)       ││
│  │    Caps cumulative duration across all retry attempts ││
│  │  ┌───────────────────────────────────────────────────┐││
│  │  │ 3. Retry Policy (Max 2 Retries, Backoff + Jitter) │││
│  │  │    Handles transient 503 / network drops          │││
│  │  │  ┌───────────────────────────────────────────────┐│││
│  │  │  │ 4. Circuit Breaker (3 failures in 10 attempts)││││
│  │  │  │    Fast-fails immediately when gateway is dead││││
│  │  │  │  ┌───────────────────────────────────────────┐││││
│  │  │  │  │ 5. Target Function (HTTP / gRPC Client)   │││││
│  │  │  │  └───────────────────────────────────────────┘││││
│  │  │  └───────────────────────────────────────────────┘│││
│  │  └───────────────────────────────────────────────────┘││
│  └───────────────────────────────────────────────────────┘│
└───────────────────────────────────────────────────────────┘
```

---

## 3. Interface Decoupling & Swappability

Every external dependency implements a distinct Go interface:

- **`PaymentGateway`** (`pkg/checkout/interfaces.go`): Interface for 3rd-party credit/debit charges.
- **`InventoryService`** (`pkg/checkout/interfaces.go`): Interface for checking, locking, and releasing inventory stock.
- **`FraudService`** (`pkg/checkout/interfaces.go`): Interface for ML risk evaluation and heuristic fallback.
- **`LoyaltyService`** (`pkg/checkout/interfaces.go`): Interface for asynchronous non-blocking rewards.
- **`TelemetryRecorder`** (`pkg/checkout/interfaces.go`): Interface for Prometheus metrics and OpenTelemetry span event hooks.

---

## 4. Running the Interactive Demo

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

## 5. Running the Parameterized Test Suite

```bash
go test -v -race ./...
```

Coverage includes:
- Parameterized matrix across normal execution, transient recovery, non-retryable 400 fast-fail, inventory rollback, and fraud degradation.
- Circuit breaker fast-fail verification.
- High-concurrency bulkhead isolation burst test under the Go race detector.
- Context cancellation propagation test.
