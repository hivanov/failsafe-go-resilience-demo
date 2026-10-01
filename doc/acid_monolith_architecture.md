# The Monolithic Alternative: Plain Old "Everything in an ACID Database"

Before adopting distributed microservices, message brokers, outbox tables, and Sagas, software architects should always evaluate the **Plain Old ACID-Compliant Database Architecture** (Single Relational Database / Modular Monolith).

For the vast majority of real-world business applications (under 50,000–500,000 active concurrent users), housing domain entities within a single ACID-compliant database (such as **PostgreSQL**, **MySQL/InnoDB**, or **CockroachDB**) completely eliminates the entire class of distributed transaction and rollback bugs.

---

## 1. Why Single-Database ACID Wins at Moderate Scale

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       MODULAR MONOLITH + ACID DATABASE                      │
│                                                                             │
│  [ Go Process: Checkout Orchestrator ]                                      │
│    │                                                                        │
│    ├── BEGIN TRANSACTION (Read Committed / Serializable)                    │
│    │     1. SELECT stock FROM inventory WHERE item_id = $1 FOR UPDATE       │
│    │     2. UPDATE inventory SET stock = stock - $qty WHERE item_id = $1    │
│    │     3. INSERT INTO orders (id, customer_id, amount) VALUES (...)       │
│    │     4. [Call External Payment API via failsafe-go Policy Onion]        │
│    │        ├── On Success: COMMIT TRANSACTION (Instant atomic state)       │
│    │        └── On Failure: ROLLBACK TRANSACTION (Automatic 0.1ms rollback) │
│    └────────────────────────────────────────────────────────────────────────┘
```

1. **Instant, Zero-Code Rollbacks (`ROLLBACK`):**
   - The database engine's **Write-Ahead Log (WAL)** and **MVCC (Multi-Version Concurrency Control)** automatically undo all modified rows, sequence updates, and locks in under 0.1ms.
   - You do **not** need to write, test, or maintain inverse compensating functions ($C_i$).
2. **Deterministic Locking & Race Prevention (`SELECT ... FOR UPDATE`):**
   - Row-level pessimistic locking prevents inventory double-allocation without needing distributed locking layers like Redis Redlock or Consul locks.
3. **Zero Dual-Write Failures:**
   - No risk of state diverging between two disjoint databases or message queues.
4. **Sub-Millisecond In-Process Calls:**
   - Communication between modules (e.g. Order $\rightarrow$ Inventory $\rightarrow$ Customer) happens via in-memory Go function calls taking $< 1\mu\text{s}$, rather than network serialization and HTTP/gRPC round-trips.

---

## 2. Architecture Comparison Matrix

| Evaluation Dimension | Plain Old ACID Relational DB | Modular Monolith (1 DB) | Distributed Microservices (Sagas) |
|---|---|---|---|
| **Rollback Complexity** | **Trivial** (`ROLLBACK`) | **Trivial** (`ROLLBACK`) | **Very High** (Manual Compensating Actions $C_i$) |
| **Consistency Model** | **Immediate Consistency** (ACID) | **Immediate Consistency** (ACID) | **Eventual Consistency** (BASE) |
| **Failure Modes** | DB connection pool exhaustion | DB connection pool exhaustion | Partial partitions, dual writes, out-of-order events |
| **Latency per Step** | $< 1\text{ms}$ (Local DB lock/index) | $< 1\text{ms}$ (Local DB lock/index) | $20\text{ms} - 150\text{ms}$ per network hop |
| **Operational Footprint** | 1 Database instance (+ Replica) | 1 Database instance (+ Replica) | Kubernetes, Kafka/NATS, CDC Relay, Tracing |
| **Recommended Scale** | **Up to 50,000 DAU** | **Up to 500,000 DAU** | **Large Multi-Team Enterprise (> 1M DAU)** |

---

## 3. Concrete Go Implementation & Live Testcontainers Verification

In this repository, the single-database ACID architecture is fully implemented and tested against a real PostgreSQL instance:

- **Database Repository Implementation:** [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go)
  - Implements row-level locking with `SELECT ... FOR UPDATE`, transactional commits, and error handling.
- **Live Testcontainers Integration Test:** [`pkg/checkout/postgres_integration_test.go`](../pkg/checkout/postgres_integration_test.go)
  - Spins up a real `postgres:16-alpine` container using `testcontainers-go`.
  - Verifies that when an external payment fails, the PostgreSQL database transaction is rolled back, preserving stock count with zero manual inverse code.
  - Verifies that under high concurrency (8 concurrent buyers), PostgreSQL's kernel-level row locks prevent double-spending without distributed lock managers.
