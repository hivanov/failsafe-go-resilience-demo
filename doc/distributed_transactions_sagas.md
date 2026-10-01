# Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks

When systems exceed the scale of a single database, or when transactions span multiple external 3rd-party SaaS providers and independent microservices with disjoint databases, single ACID transactions are physically impossible.

---

## Table of Contents

- [1. The Distributed Transaction Dilemma: 2PC vs. Sagas](#1-the-distributed-transaction-dilemma-2pc-vs-sagas)
- [2. The Saga Pattern: Forward Transactions (Ti) & Compensations (Ci)](#2-the-saga-pattern-forward-transactions-t_i--compensations-c_i)
- [3. Orchestrated vs. Choreographed Sagas](#3-orchestrated-vs-choreographed-sagas)
- [4. The 4 Golden Rules of Distributed Rollbacks & Compensations](#4-the-4-golden-rules-of-distributed-rollbacks--compensations)
- [5. Curated Deep-Dive Resources: Sagas & Distributed Rollbacks](#5-curated-deep-dive-resources-sagas--distributed-rollbacks)
  - [Foundational Books](#foundational-books)
  - [Seminal Research Papers](#seminal-research-papers)
  - [Production Go Distributed Transaction Frameworks & SDKs](#production-go-distributed-transaction-frameworks--sdks)

---

## 1. The Distributed Transaction Dilemma: 2PC vs. Sagas

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

In high-throughput distributed systems, **Two-Phase Commit (2PC)** is widely considered an anti-pattern because holding locks across network hops destroys availability and throughput. Instead, modern microservice architectures adopt **Eventual Consistency** via the **Saga Pattern**.

---

## 2. The Saga Pattern: Forward Transactions ($T_i$) & Compensations ($C_i$)

A Saga is a sequence of local transactions:
- For every forward step $T_i$ executed in a service, there is a corresponding **Compensating Transaction** $C_i$ designed to undo its semantic side-effects if a subsequent step $T_{i+1}$ permanently fails.

$$\text{Forward Flow: } T_1 \longrightarrow T_2 \longrightarrow T_3 \dots \longrightarrow T_n$$
$$\text{Rollback Flow (if } T_3 \text{ fails): } T_1 \longrightarrow T_2 \longrightarrow T_3 (\text{FAIL}) \Longrightarrow C_2 \longrightarrow C_1$$

### In Our Checkout Service:
1. $T_1$ (`FraudService.EvaluateRisk`): Read-only evaluation (no compensation required).
2. $T_2$ (`InventoryService.LockInventory`): Decrements available stock from warehouse.
3. $T_3$ (`PaymentGateway.Charge`): Charges the customer credit card.
4. **Failure Trigger:** If $T_3$ fails with a non-retryable error (e.g. `ErrInvalidPayment` 400), the orchestrator immediately triggers **$C_2$ (`InventoryService.ReleaseInventory`)** to restore the locked stock.

---

## 3. Orchestrated vs. Choreographed Sagas

1. **Orchestrated Saga (Command-Driven):**
   - A central coordinator (such as our Go `Orchestrator` in [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go) or an engine like **Temporal / Cadence**) explicitly invokes each service via RPC/HTTP and records state transitions. If an unrecoverable failure occurs, the orchestrator invokes compensating activities in reverse order.
   - *Best for:* Complex flows with strict SLA budgets, clear auditing requirements, and central time coordination.

2. **Choreographed Saga (Event-Driven):**
   - Services communicate by publishing and subscribing to domain events over a broker (Kafka, Solace, RabbitMQ, NATS).
   - E.g., `OrderService` emits `OrderCreated` $\rightarrow$ `InventoryService` consumes, locks stock, emits `InventoryLocked` $\rightarrow$ `PaymentService` consumes, charges card, emits `PaymentFailed` $\rightarrow$ `InventoryService` consumes `PaymentFailed` and executes compensation.
   - *Best for:* Simple pipelines with few participants and high decoupling needs.

---

## 4. The 4 Golden Rules of Distributed Rollbacks & Compensations

1. **Compensations MUST Be Idempotent:**
   - In distributed systems, network retries can deliver compensation commands multiple times. Executing $C_i$ (e.g., `ReleaseInventory`) 3 times must have the exact same effect as executing it once.
2. **Handle Out-of-Order Message Arrival:**
   - Due to network reordering, a compensation message ($C_i$) can arrive *before* the original forward command ($T_i$). Services must record a "cancel-pending" state so when $T_i$ eventually arrives, it is immediately discarded.
3. **Transactional Outbox Pattern (Dual-Write Prevention):**
   - Never write to a database and publish to a message broker as two separate, non-transactional operations. Store outgoing messages in a local database `outbox` table within the same ACID transaction as the business entity, and use a CDC (Change Data Capture) relay (e.g., Debezium) to publish to the broker.
4. **Compensations Cannot Fail (Must Retry to Completion):**
   - If a forward step fails, the business can abort. But if a *compensation* fails (e.g., DB temporarily down during inventory release), the system cannot give up. Compensations must be retried with exponential backoff until they succeed or are flagged for human operator intervention.

---

## 5. Curated Deep-Dive Resources: Sagas & Distributed Rollbacks

### Foundational Books:
- 📖 **"Microservices Patterns: With examples in Java"** by *Chris Richardson* (Manning Publications)
  - *Chapters 4 & 5:* The definitive guide on Sagas, Orchestration vs. Choreography, and Compensating Transactions.
- 📖 **"Designing Data-Intensive Applications (DDIA)"** by *Martin Kleppmann* (O'Reilly Media)
  - *Chapters 7, 8 & 9:* Deep analysis of Distributed Transactions, Dual-Writes, Atomic Commit, Linearizability, and Two-Phase Commit limitations.
- 📖 **"Building Microservices (2nd Edition)"** by *Sam Newman* (O'Reilly Media)
  - *Chapter 6:* Distributed Transactions, Sagas, Eventual Consistency, and Async Coordination.
- 📖 **"Enterprise Integration Patterns"** by *Gregor Hohpe & Bobby Woolf* (Addison-Wesley)
  - Covers Process Manager, Routing Slip, and Compensating Message Router patterns.

### Seminal Research Papers:
- 📄 **"Sagas" (1987)** by *Hector Garcia-Molina & Kenneth Salem* (Princeton University)
  - The foundational paper introducing the Saga concept for long-lived transactions (LLTs). Available via ACM Digital Library.
- 📄 **"Life beyond Distributed Transactions: an Apostate’s Opinion" (2007)** by *Pat Helland* (Amazon / Microsoft)
  - Explains why distributed transactions fail to scale in cloud environments and how entities, idempotency, and messaging replace 2PC.

### Production Go Distributed Transaction Frameworks & SDKs:
- 🛠️ **Temporal Go SDK** (`go.temporal.io/sdk`): [temporal.io](https://temporal.io)
  - The premier workflow-as-code orchestration engine in Go. Workflows automatically track activity state, persist execution histories, and trigger compensating activities upon failure.
- 🛠️ **DTM (Distributed Transaction Manager in Go)**: [github.com/dtm-labs/dtm](https://github.com/dtm-labs/dtm)
  - High-performance Go distributed transaction framework supporting **SAGA**, **TCC (Try-Confirm-Cancel)**, and **XA/2PC** with built-in sub-transaction barrier technology to prevent out-of-order execution and null compensations.
- 🛠️ **Cadence Go Client** (`go.uber.org/cadence`): [github.com/uber-go/cadence-client](https://github.com/uber-go/cadence-client)
  - Uber's distributed workflow orchestration engine for long-running, fault-tolerant business transactions.
