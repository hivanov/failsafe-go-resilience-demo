# Context Propagation & Socket Leak Prevention in Go

In a high-throughput, fault-tolerant Go application, **every outbound dependency and activity interface MUST accept `context.Context` as its first parameter and strictly honor cancellation signals.**

Without context propagation, when a `failsafe-go` Operation Timeout or Per-Attempt Timeout fires:
- The orchestrator moves on, but the underlying goroutine and TCP socket remain active in the background.
- Memory buffers, connection pool slots, and file descriptors remain locked, leading to **silent resource exhaustion and cascading server crashes**.

---

## 1. The 3 Rules of Context-Aware Resilience

1. **Pass `exec.Context()` to Outbound Calls:**
   - When invoking downstream services inside a failsafe executor, always pass `exec.Context()` down the call stack. Failsafe-go attaches its active attempt and overall timeouts to this context.
   - Code reference: [`pkg/policies/payment_policy.go`](../pkg/policies/payment_policy.go).

2. **Close Network Resources on Cancellation:**
   - Use `http.NewRequestWithContext` or standard database drivers with `ExecContext`/`QueryContext` so that socket disconnects immediately abort in-flight TCP streams.
   - Code reference: [`pkg/checkout/interfaces.go`](../pkg/checkout/interfaces.go).

3. **Check `ctx.Done()` in Long-Running Operations:**
   - For compute-heavy or batched activities, periodically check `ctx.Done()` or `ctx.Err()` to exit early before doing wasted computation.
   - Code reference: [`pkg/checkout/orchestrator.go`](../pkg/checkout/orchestrator.go) (checks `ctx.Err()` before starting sequential pipeline).

---

## 2. Implementation in Downstream Drivers

- **Simulated Payment Gateway:** [`pkg/downstream/payment_gateway.go`](../pkg/downstream/payment_gateway.go)
  - Uses `select { case <-time.After(lat): case <-ctx.Done(): return ..., ctx.Err() }` to simulate immediate socket cancellation.
- **PostgreSQL Inventory Repository:** [`pkg/downstream/postgres_inventory.go`](../pkg/downstream/postgres_inventory.go)
  - Uses `BeginTx(ctx, ...)`, `QueryRowContext(ctx, ...)`, and `ExecContext(ctx, ...)` ensuring slow DB queries are canceled at the PostgreSQL server level.
- **Context Cancellation Unit Test:** [`pkg/checkout/orchestrator_test.go`](../pkg/checkout/orchestrator_test.go) (`TestContext_Cancellation`)
  - Verifies that canceling the client context terminates in-flight work in $< 40\text{ms}$.
