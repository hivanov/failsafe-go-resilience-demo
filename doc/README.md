# Deep-Dive Documentation Index

This directory contains in-depth architectural guides, distributed systems patterns, database consistency models, resilience engineering methodologies, operational support processes, infrastructure resource governance, and comprehensive literature references supporting the `failsafe-go` demonstration.

---

## Table of Contents

- [1. Resilience Design Process & Modularity Principles in Go](./design_process_and_modularity.md)
- [2. Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)
- [3. Coupling Software Resilience with Operational Support Processes](./operational_resilience_and_support_processes.md)
- [4. Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)
- [5. Disjoint Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)
- [6. Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)
- [7. Stakeholder Interview Framework & Caching Fallback Architectures in Go](./product_owner_interview_and_caching_fallbacks.md)
- [8. Workflow Engines vs. In-Process Go Orchestration: Trade-offs & Unit Economics](./workflow_engines_vs_code_orchestration.md)
- [9. The Economics of Resilience: SLA Cost Scaling, SDLC Attribution & Maturity Governance](./resilience_economics_and_maturity_levels.md)
- [10. Software & Hardware Limits: Capacity Budgeting & Resource Governance](./software_hardware_limits_and_capacity_budgeting.md)
- [11. Further Reading, Bibliography & Historical Media](./further_reading_and_bibliography.md)

---

## Document Index

1. **[Resilience Design Process & Modularity Principles in Go](./design_process_and_modularity.md)**
   - The 80/20 Pareto rule in resilience engineering; The 7-phase iterative design cycle (SLA contract $\rightarrow$ classification $\rightarrow$ math budgeting $\rightarrow$ resource governance $\rightarrow$ policy composition $\rightarrow$ decorator modularity $\rightarrow$ telemetry tuning loop); Resource governance deep-dive: Kubernetes CPU limits/requests (CFS throttling mitigation & `automaxprocs`), Memory limits & `GOMEMLIMIT` calibration, Open Socket & File Descriptor pools, GPU VRAM allocation & bulkheads, and Linux swap elimination; Go interface segregation and context-first contracts.

2. **[Architectural Anti-Patterns in Resilient System Design](./anti_patterns.md)**
   - Exhaustive analysis and remediation of 20 fatal resilience anti-patterns: Unbounded Retries, Retries Without Jitter, Retrying Non-Idempotent Operations, Locking Pitfalls & Deadlocks, Inadequate Testing, Lack of Telemetry, Lack of Business Awareness, Over-Engineering SLAs (Five-Nines Fantasy), Lack of Fallbacks, Ignoring Critical Path, Hunch-Based Timeouts, Context Disconnection, Blind Error Retries, Cascading Shared Breakers, Kubernetes CPU CFS Throttling, Missing `GOMEMLIMIT` (OOMKill Exit Code 137), Ephemeral Port & Socket Leaks, Goroutine Heap Explosions, GPU VRAM Saturation, and Swap Thrashing / GC Pauses.

3. **[Coupling Software Resilience with Operational Support Processes](./operational_resilience_and_support_processes.md)**
   - Expecting failure as normal operations (Safe-to-Fail); Support personnel ownership and on-call rotations; Actionable symptom-based alerting and zero alert fatigue; Progressive remediation (Manual Runbooks $\rightarrow$ GameDays/Drills $\rightarrow$ Automated Self-Healing); Blameless post-mortem feedback loops.

4. **[Single-Database ACID Architecture vs. Distributed Microservices](./acid_monolith_architecture.md)**
   - The modular monolithic alternative: instant zero-code rollbacks (`ROLLBACK`), deterministic row-level locking (`SELECT ... FOR UPDATE`), zero dual-write bugs, and scale comparison matrix (< 50,000 to > 1,000,000 DAU).
   - Code reference: [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go) & [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go).

5. **[Distributed Microservice Strategies: Eventual Consistency, Sagas & Distributed Rollbacks](./distributed_transactions_sagas.md)**
   - Why 2PC fails in cloud microservices; Forward ($T_i$) vs. Compensating ($C_i$) actions; Orchestrated vs. Choreographed Sagas; The 4 Golden Rules of Distributed Rollbacks; Transactional Outbox pattern; In-memory Go Saga coordinator.
   - Core literature: *Microservices Patterns* (Chris Richardson), *DDIA* (Martin Kleppmann), *Building Microservices* (Sam Newman), 1987 Sagas paper (Garcia-Molina & Salem).

6. **[Context Propagation & Socket Leak Prevention in Go](./context_cancellation_best_practices.md)**
   - How `exec.Context()` bridges `failsafe-go` timeouts to underlying OS network sockets, `http.Client`, database drivers, and goroutines to eliminate silent server resource exhaustion.
   - Code reference: [`pkg/checkout/interfaces.go`](../pkg/checkout/interfaces.go) & [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go).

7. **[Stakeholder Interview Framework & Caching Fallback Architectures in Go](./product_owner_interview_and_caching_fallbacks.md)**
   - The 4 mandatory Product Owner interview questions (Degraded behavior, Error classification, SLA & user patience budgets, Financial cost & risk trade-offs); Translation matrix from business answers to `failsafe-go` policy composition; Multi-tier caching fallback architectures: Tier 0 Edge CDN whole request-response caching (RFC 5861 `stale-if-error`, `stale-while-revalidate`, ETag conditional requests), Tier 1 Remote Redis/MongoDB caches with singleflight stampede suppression and cache circuit breakers, Tier 2 In-memory local caches (`sync.RWMutex` with bounded TTL), and Tier 3 Event-synchronized in-memory cache over Solace PubSub+ and Kafka guaranteed messaging.
   - Code reference: [`pkg/checkout/interfaces.go`](../pkg/checkout/interfaces.go).

8. **[Workflow Engines vs. In-Process Go Orchestration: Trade-offs & Unit Economics](./workflow_engines_vs_code_orchestration.md)**
   - The fallacy of "No-Code Resilience": Why visual engines (Google Cloud Workflows, Microsoft PowerAutomate, AWS Step Functions, Azure Logic Apps, Camunda) do NOT relieve developers from policy design; Comprehensive trade-off matrix: Sub-microsecond latency vs 200ms step overhead, near-zero cost vs $25/M transition billing, type safety & IDE refactoring vs YAML/JSON DSL hell, local testing via Testcontainers vs cloud sandboxes; In-flight workflow version drift and schema migration challenges; Two-tier hybrid architecture (Temporal/Step Functions coarse-grained durable lifecycle + `failsafe-go` fine-grained synchronous hot-path execution).

9. **[The Economics of Resilience: SLA Cost Scaling, SDLC Attribution & Maturity Governance](./resilience_economics_and_maturity_levels.md)**
   - The non-linear cost curve of availability ($Cost \propto \frac{1}{1 - \text{SLA}}$); Resilience cost attribution breakdown across Architecture & Infrastructure (35–40%), SDLC & Quality Gates (25–30%), Deployment Engineering (15–20%), Observability & Business KPIs (10–15%), and Operational Support (10–15%); Transitioning from misleading technical metrics (the "Silent 200 OK") to real-time Business SLIs (order velocity, revenue flow rate, fallback activation ratios); The 4-Stage Software Maturity Model (**PoC $\rightarrow$ Pilot $\rightarrow$ Production $\rightarrow$ Scaling**) with formal technical and business transition gate checklists; Business Capability Criticality Scoring (BCCS) and the economic stop-mechanism ($\Delta \text{CoR} \le \Delta \text{ALE}$).

10. **[Software & Hardware Limits: Capacity Budgeting & Resource Governance](./software_hardware_limits_and_capacity_budgeting.md)**
    - Physical machine and OS limits: File descriptors (`ulimit -n`), TCP ephemeral port exhaustion (TIME_WAIT retention), PostgreSQL connection pool contention, goroutine heap expansion, and kernel socket buffers; Resource lifecycle during retries (allocation, hold times, and leak prevention via `resp.Body.Close()`); The Resource Budgeting Spreadsheet per operation; Mathematical capacity planning across concurrent workload mixes ($X$ Checkouts, $Y$ Searches); Why load testing is an experimental proof of an architectural model rather than an exploratory failure discovery mechanism; The "Blind Horizontal Scaling" disaster (scaling stateless pods into database collapse); Real-time Headroom SLIs.

11. **[Further Reading, Bibliography & Historical Media](./further_reading_and_bibliography.md)**
    - Master bibliography (*Release It!*, *DDIA*, *Tanenbaum Distributed Systems*, *TailoredRead Eventual Consistency*, *Microservices Patterns*, *SRE Book*); Pioneer conference talks & media (Jesse Robbins: *Operations at Web Scale*, *GameDay: Master of Disaster*; John Allspaw: *10+ Deploys Per Day*); RFC 9111 HTTP caching & ETag validation; `golang.org/x/sync/singleflight` stampede mitigation patterns; Repeatable testing methodologies (Testcontainers, virtual clocks `clockwork`, deterministic PRNG jitter seeding, concurrency race testing under `-race`).
