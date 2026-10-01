# Deep-Dive Documentation Index

This directory contains in-depth architectural guides, distributed systems patterns, database consistency models, and resilience literature references supporting the `failsafe-go` demonstration.

---

## Table of Contents

- [1. Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)
- [2. Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)
- [3. Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)
- [4. Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)

---

## Document Index

1. **[Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)**
   - Analysis of 10 fatal resilience anti-patterns: Unbounded Retries, Retries Without Jitter, Retrying Non-Idempotent Operations, Lack of Fallbacks, Untested Policies, Ignoring the Critical Path, Hunch-Based Timeouts, Context Disconnection, Blind Error Retries, and Cascading Shared Breakers.

2. **[Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)**
   - The modular monolithic alternative: instant zero-code rollbacks (`ROLLBACK`), deterministic row-level locking (`SELECT ... FOR UPDATE`), zero dual-write bugs, and scale comparison matrix (< 50,000 to > 1,000,000 DAU).
   - Code reference: [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go) & [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go).

3. **[Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)**
   - Why 2PC fails in cloud microservices; Forward ($T_i$) vs. Compensating ($C_i$) actions; Orchestrated vs. Choreographed Sagas; The 4 Golden Rules of Distributed Rollbacks; Transactional Outbox pattern; In-memory Go Saga coordinator.
   - Comprehensive literature references: Books (*Microservices Patterns*, *DDIA*, *Building Microservices*, *EIP*), Seminal Papers (1987 Sagas, 2007 Pat Helland), and Production Go Frameworks (Temporal Go SDK, DTM, Cadence).

4. **[Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)**
   - How `exec.Context()` bridges `failsafe-go` timeouts to underlying OS network sockets, `http.Client`, database drivers, and goroutines to eliminate silent server resource exhaustion.
   - Code reference: [`pkg/checkout/interfaces.go`](../pkg/checkout/interfaces.go).
