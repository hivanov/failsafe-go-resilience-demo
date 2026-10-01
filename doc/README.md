# Deep-Dive Documentation Index

This directory contains in-depth architectural guides, distributed systems patterns, database consistency models, resilience engineering methodologies, operational support processes, and comprehensive literature references supporting the `failsafe-go` demonstration.

---

## Table of Contents

- [1. Resilience Design Process & Modularity Principles in Go](./design_process_and_modularity.md)
- [2. Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)
- [3. Coupling Software Resilience with Operational Support Processes](./operational_resilience_and_support_processes.md)
- [4. Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)
- [5. Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)
- [6. Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)
- [7. Further Reading, Bibliography & Historical Media](./further_reading_and_bibliography.md)

---

## Document Index

1. **[Resilience Design Process & Modularity Principles in Go](./design_process_and_modularity.md)**
   - The 80/20 Pareto rule in resilience engineering; The 6-phase iterative design cycle (SLA contract $\rightarrow$ classification $\rightarrow$ math budgeting $\rightarrow$ policy composition $\rightarrow$ decorator modularity $\rightarrow$ telemetry tuning loop); Go interface segregation and context-first design principles.

2. **[Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)**
   - Exhaustive analysis and remediation of 14 fatal resilience anti-patterns: Unbounded Retries, Retries Without Jitter, Retrying Non-Idempotent Operations, Locking Pitfalls & Deadlocks, Inadequate Testing, Lack of Telemetry, Lack of Business Awareness, Over-Engineering SLAs (Five-Nines Fantasy), Lack of Fallbacks, Ignoring Critical Path, Hunch-Based Timeouts, Context Disconnection, Blind Error Retries, and Cascading Shared Breakers.

3. **[Coupling Software Resilience with Operational Support Processes](./operational_resilience_and_support_processes.md)**
   - Expecting failure as normal operations (Safe-to-Fail); Support personnel ownership and on-call rotations; Actionable symptom-based alerting and zero alert fatigue; Progressive remediation (Manual Runbooks $\rightarrow$ GameDays/Drills $\rightarrow$ Automated Self-Healing); Blameless post-mortem feedback loops.

4. **[Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)**
   - The modular monolithic alternative: instant zero-code rollbacks (`ROLLBACK`), deterministic row-level locking (`SELECT ... FOR UPDATE`), zero dual-write bugs, and scale comparison matrix (< 50,000 to > 1,000,000 DAU).
   - Code reference: [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go) & [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go).

5. **[Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)**
   - Why 2PC fails in cloud microservices; Forward ($T_i$) vs. Compensating ($C_i$) actions; Orchestrated vs. Choreographed Sagas; The 4 Golden Rules of Distributed Rollbacks; Transactional Outbox pattern; In-memory Go Saga coordinator.
   - Core literature: *Microservices Patterns* (Chris Richardson), *DDIA* (Martin Kleppmann), *Building Microservices* (Sam Newman), 1987 Sagas paper (Garcia-Molina & Salem).

6. **[Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)**
   - How `exec.Context()` bridges `failsafe-go` timeouts to underlying OS network sockets, `http.Client`, database drivers, and goroutines to eliminate silent server resource exhaustion.
   - Code reference: [`pkg/checkout/interfaces.go`](../pkg/checkout/interfaces.go) & [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go).

7. **[Further Reading, Bibliography & Historical Media](./further_reading_and_bibliography.md)**
   - Master bibliography (*Release It!*, *DDIA*, *Tanenbaum Distributed Systems*, *TailoredRead Eventual Consistency*, *Microservices Patterns*, *SRE Book*); Pioneer conference talks & media (Jesse Robbins: *Operations at Web Scale*, *GameDay: Master of Disaster*; John Allspaw: *10+ Deploys Per Day*); Repeatable testing methodologies (Testcontainers, virtual clocks `clockwork`, deterministic PRNG jitter seeding, concurrency race testing under `-race`).
