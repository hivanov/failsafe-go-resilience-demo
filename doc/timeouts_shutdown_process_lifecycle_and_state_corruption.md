# Timeouts, Process Lifecycle, Graceful Shutdown & Corrupted State Detection

This document provides an exhaustive architectural and systems-level guide to timeout governance, process isolation, shutdown mechanics, orphan/zombie process elimination, and detecting and recovering from corrupted states in distributed and in-process Go architectures.

---

## Table of Contents

- [1. The Duality of Timeouts: In-Your-Control vs. External Deadlines](#1-the-duality-of-timeouts-in-your-control-vs-external-deadlines)
  - [1.1 In-Your-Control Timeouts (Internal Governance)](#11-in-your-control-timeouts-internal-governance)
  - [1.2 Ungraceful / External Deadlines (Caller & Ingress Aborts)](#12-ungraceful--external-deadlines-caller--ingress-aborts)
- [2. Graceful Shutdown vs. Ungraceful Termination](#2-graceful-shutdown-vs-ungraceful-termination)
  - [2.1 The Complete Kubernetes Pod Termination Sequence](#21-the-complete-kubernetes-pod-termination-sequence)
  - [2.2 In-Flight Request Draining Pattern in Go](#22-in-flight-request-draining-pattern-in-go)
  - [2.3 Ungraceful Teardown: SIGKILL, Kernel OOM, and Storage Cuts](#23-ungraceful-teardown-sigkill-kernel-oom-and-storage-cuts)
- [3. The Zombie & Orphan Task Problem: Wasted Compute & Thread Exhaustion](#3-the-zombie--orphan-task-problem-wasted-compute--thread-exhaustion)
  - [3.1 The "Ghost Goroutine" Anti-Pattern](#31-the-ghost-goroutine-anti-pattern)
  - [3.2 The Upstream Disconnect Trap](#32-the-upstream-disconnect-trap)
- [4. Hard Process Termination & Isolation Mechanisms](#4-hard-process-termination--isolation-mechanisms)
  - [4.1 Cooperative Cancellation (`context.Context` & I/O Deadlines)](#41-cooperative-cancellation-contextcontext--io-deadlines)
  - [4.2 OS-Level Subprocess Termination: `fork()`, Process Groups & `SIGKILL`](#42-os-level-subprocess-termination-fork-process-groups--sigkill)
  - [4.3 Supervisor Trees & Watchdog Monitors](#43-supervisor-trees--watchdog-monitors)
  - [4.4 WebAssembly (WASI) Fuel Metering & Hard Memory Traps](#44-webassembly-wasi-fuel-metering--hard-memory-traps)
- [5. Corrupted State Detection in Timeout & Crash Scenarios](#5-corrupted-state-detection-in-timeout--crash-scenarios)
  - [5.1 The Ambiguity of the Network Timeout (The Schrödinger's Request)](#51-the-ambiguity-of-the-network-timeout-the-schrödingers-request)
  - [5.2 Heuristic Indicators of Hidden State Corruption](#52-heuristic-indicators-of-hidden-state-corruption)
  - [5.3 Non-Transactional Multi-Step Mutations](#53-non-transactional-multi-step-mutations)
- [6. State Recovery & Self-Healing Architectures](#6-state-recovery--self-healing-architectures)
  - [6.1 Crash-Only Idempotent Replay](#61-crash-only-idempotent-replay)
  - [6.2 Asynchronous Background Reconciliation (The Sweeper Pattern)](#62-asynchronous-background-reconciliation-the-sweeper-pattern)
  - [6.3 Compensating Sagas with State Probing](#63-compensating-sagas-with-state-probing)

---

## 1. The Duality of Timeouts: In-Your-Control vs. External Deadlines

Timeouts are not merely static timer values; they operate in two distinct domains: **internal proactive governance** and **external reactive enforcement**.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             INGRESS / CALLER                                │
│  Enforces: Client Timeout / API Gateway SLA Budget (e.g. 1000ms)            │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            APPLICATION SERVICE                              │
│                                                                             │
│  [ In-Your-Control Timeouts ]           [ Ungraceful / External Deadlines ] │
│  ├── Attempt Timeout (e.g. 250ms)       ├── Caller Client Drop / Cancel     │
│  ├── Database Lock Timeout (e.g. 100ms) ├── Kubernetes TerminationGracePeriod│
│  ├── Graceful Shutdown Drain (e.g. 15s) └── Gateway 504 Gateway Timeout    │
│  └── Subprocess Worker Watchdog         │                                   │
│                                         │                                   │
│  [ Policy Execution & Cancellation ] ───┘                                   │
│  ├── Propagates `ctx.Done()` downwards                                      │
│  ├── Aborts network sockets & DB queries                                    │
│  └── Halts child goroutines / processes                                     │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 1.1 In-Your-Control Timeouts (Internal Governance)
These are deadlines established intentionally by your service to protect its internal SLA and resource pools:
1. **Per-Attempt Timeout**: Bounding a single network socket call (e.g., $200\text{ms}$) so a slow downstream peer does not starve the thread.
2. **Overall Operation Timeout**: Bounding the cumulative duration across all retry attempts combined (e.g., $700\text{ms}$).
3. **Database Lock Timeout (`lock_timeout` / `statement_timeout`)**: Halting a SQL transaction if a row lock cannot be acquired within $100\text{ms}$, preventing connection pool exhaustion.
4. **Shutdown Draining Timeout**: Allowing in-flight HTTP requests $15\text{s}$ to finish writing responses before the server process exits.

### 1.2 Ungraceful / External Deadlines (Caller & Ingress Aborts)
These occur when an external entity severs the connection before your service finishes execution:
- **Client Disconnection**: A mobile app user closes their browser or loses cell reception while a checkout operation is executing.
- **Upstream Gateway Abort**: An Ingress controller (Envoy, NGINX) hits its own hard timeout and closes the downstream TCP socket.
- **Orchestrator Eviction**: Kubernetes sends `SIGTERM` because a node is being drained or a Horizontal Pod Autoscaler (HPA) is scaling down.

---

## 2. Graceful Shutdown vs. Ungraceful Termination

### 2.1 The Complete Kubernetes Pod Termination Sequence
When Kubernetes shuts down a pod, there is a race between endpoint deregistration and process termination:

```
1. Pod marked Terminating ──┬──> 2. Removed from Endpoints / kube-proxy / CoreDNS
                           │
                           └───> 3. `preStop` hook executes (e.g. sleep 5s)
                                       │
                                       ▼
                                 4. `SIGTERM` sent to process (PID 1)
                                       │
                                       ▼
                                 5. Service stops accepting new traffic (returns 503 or closes listener)
                                       │
                                       ▼
                                 6. In-flight requests drained (within terminationGracePeriodSeconds)
                                       │
                                       ▼
                                 7. If still running after grace period: `SIGKILL` (Uncatchable)
```

**Crucial Pitfall**: If your application exits immediately upon receiving `SIGTERM`, clients whose requests were already routed by kube-proxy will receive TCP `Connection Refused` errors. Always introduce a brief draining delay (or Kubernetes `preStop` sleep) to allow routing tables to clear.

### 2.2 In-Flight Request Draining Pattern in Go
Idiomatic Go implementation for managing graceful server teardown:

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func RunServer(ctx context.Context, handler http.Handler, port string) error {
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Server listening on port %s", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// Listen for OS termination signals
	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case sig := <-shutdownSignal:
		log.Printf("Received signal %v: initiating graceful shutdown", sig)

		// Create a bounded drain context (e.g. 15 seconds)
		drainCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Shutdown stops accepting new conns and waits for active requests to finish
		if err := server.Shutdown(drainCtx); err != nil {
			log.Printf("Forced shutdown triggered: %v", err)
			_ = server.Close() // Force-close active underlying TCP listeners
			return err
		}
		log.Println("Graceful shutdown completed successfully")
	}

	return nil
}
```

### 2.3 Ungraceful Teardown: SIGKILL, Kernel OOM, and Storage Cuts
When a process is terminated ungracefully (`SIGKILL`, kernel OOM killer Exit Code 137, power failure, VM crash):
- No deferred cleanup handlers (`defer`) execute.
- Open TCP sockets are reset via kernel TCP `RST` packets.
- In-memory cache buffers and unwritten write-ahead logs in RAM are permanently lost.
- **Architectural Requirement**: Applications must be **Crash-Only Software**—the system must recover to a consistent state upon restart purely by inspecting persistent disk/database state without requiring clean shutdown artifacts.

---

## 3. The Zombie & Orphan Task Problem: Wasted Compute & Thread Exhaustion

### 3.1 The "Ghost Goroutine" Anti-Pattern
When an upstream HTTP request is cancelled or times out, but downstream worker goroutines do not observe the cancellation, they become **ghost goroutines**. They continue executing expensive cryptographic operations, database queries, and ML inferences whose results will immediately be discarded.

```go
// ANTI-PATTERN: Orphaned goroutine compute waste
func ProcessOrder(ctx context.Context, orderID string) (Result, error) {
    ch := make(chan Result)
    
    // BAD: This goroutine runs to completion even if ctx times out!
    go func() {
        // Expensive 5-second computation / DB query
        res := performHeavyCompute(orderID) 
        ch <- res // Leaks goroutine if nobody is reading from ch!
    }()

    select {
    case <-ctx.Done():
        // Returns 504 / ContextDeadlineExceeded to client
        // BUT the background goroutine keeps burning 100% CPU in the background!
        return Result{}, ctx.Err()
    case res := <-ch:
        return res, nil
    }
}
```

### 3.2 The Upstream Disconnect Trap
In long-running HTTP endpoints (file downloads, heavy reporting, SSE event streams), if the client closes the connection, the Go HTTP server notifies the handler via `r.Context().Done()`. If the handler ignores `r.Context()`, the server continues generating the report and holding database connections until completion.

---

## 4. Hard Process Termination & Isolation Mechanisms

In Go, goroutines cannot be externally killed; Go has no `goroutine.Kill()` or thread termination API. Cancellation must either be **cooperative** or isolated behind **OS process boundaries** or **virtual sandboxes**.

### 4.1 Cooperative Cancellation (`context.Context` & I/O Deadlines)
The standard Go idiom relies on tight cooperative cancellation loops:
1. Check `ctx.Err()` at loop iterations and before expensive calls.
2. Pass `ctx` directly to standard library network/database calls (`http.NewRequestWithContext`, `db.QueryContext`).
3. Set low-level read/write deadlines on network connections (`net.Conn.SetDeadline`).

### 4.2 OS-Level Subprocess Termination: `fork()`, Process Groups & `SIGKILL`
When executing untrusted, third-party, or heavy computational tasks (e.g. C/C++ extensions, Python scripts, ffmpeg transcoding, user-submitted code), cooperative cancellation is insufficient because rogue code can block indefinitely or ignore signals.

**Solution: Subprocess Isolation with Dedicated Process Groups (`setpgid`)**:

```go
package supervisor

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// RunIsolatedCommand executes a binary with strict hard-kill process group semantics.
func RunIsolatedCommand(parentCtx context.Context, timeout time.Duration, binaryPath string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)

	// CRITICAL: Put the child process into its own Process Group (PGID).
	// Without this, killing cmd.Process only kills the parent wrapper,
	// leaving child sub-processes (orphaned grand-children) running as zombies.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Override default Cancel behavior to kill the ENTIRE process group
	cmd.Cancel = func() error {
		// Negative PGID signals the entire process group
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
		return cmd.Process.Kill()
	}

	return cmd.CombinedOutput()
}
```

### 4.3 Supervisor Trees & Watchdog Monitors
For long-running background workers:
- A parent supervisor process monitors child worker heartbeats over shared IPC/channels.
- If a child worker misses 3 consecutive heartbeats ($3 \times 500\text{ms}$), the supervisor forcibly terminates the process (`SIGKILL`), cleans up associated shared locks, and spawns a fresh worker replica.

### 4.4 WebAssembly (WASI) Fuel Metering & Hard Memory Traps
In environments running user-defined logic or multi-tenant code (such as in-browser or edge engines):
- Use **WebAssembly (WASI Component Model)** runtimes (e.g., Wasmtime, Wazero).
- **Deterministic Fuel Consumption**: Inject instructions into the WASM bytecode that decrement a fuel counter on every CPU instruction. When fuel reaches 0, the WASM engine immediately traps and aborts execution in $< 1\mu\text{s}$ with zero CPU leakage.
- **Strict Memory Capping**: WASM linear memory is bounded to a fixed page count (e.g. $64\text{MB}$). Attempts to allocate beyond the limit trigger an uncatchable runtime trap, isolating the host process.

---

## 5. Corrupted State Detection in Timeout & Crash Scenarios

### 5.1 The Ambiguity of the Network Timeout (The Schrödinger's Request)
When an HTTP request or RPC times out, the client has **zero information** about what occurred on the server:

```
CLIENT                              NETWORK                             SERVER
  │                                    │                                  │
  ├─── POST /v1/payments/charge ───────┼─────────────────────────────────>│ (Received)
  │    (OrderID: 12345, $100)          │                                  │
  │                                    │                                  ├── Deduct Balance
  │                                    │                                  ├── Insert DB Row
  │                                    │                                  └── Dispatch Webhook
  │                                    │        [ 504 / Packet Drop ]     │
  │<────────── TIMEOUT (800ms) ────────┼──────────────────────────────────┤ (Success Response Lost)
  │                                    │                                  │
  ▼                                                                       ▼
Did payment succeed or fail?                                    Payment COMMITTED!
```

If the client assumes the operation failed and immediately retries without an idempotency key, the customer is **charged twice** (State Corruption).

### 5.2 Heuristic Indicators of Hidden State Corruption
How can an engineer or QA detect whether a system is vulnerable to state corruption, even if documentation claims the API is "resilient"?

| Heuristic Marker | Risk Description | Corruption Vulnerability |
| :--- | :--- | :--- |
| **Missing Idempotency Key in API Schema** | The endpoint does not accept an `Idempotency-Key` or `X-Request-ID` header. | **EXTREME**: Any retry after a network timeout will create duplicate entities or double-charge. |
| **HTTP Status 500 on Connection Drop** | Downstream system returns generic 500 Internal Error when a secondary call fails. | **HIGH**: Indicates partial execution occurred before failure without transactional rollback. |
| **Multi-Resource Mutation Without ACID** | A handler updates a SQL database, then calls an external email/SMS API, then calls Redis. | **HIGH**: If the process crashes or times out after SQL commit but before Redis/SMS, state is fractured. |
| **Non-Atomic "Check-Then-Act" APIs** | API provides `GET /balance` followed by `POST /deduct` rather than atomic CAS/conditional writes. | **EXTREME**: Race conditions and concurrent timeouts lead to negative balances. |
| **Missing Database Unique Constraints** | Tables rely purely on application-level uniqueness checks without SQL `UNIQUE` indexes. | **CRITICAL**: Concurrent timeout retries insert duplicate rows simultaneously. |

### 5.3 Non-Transactional Multi-Step Mutations
When an operation must mutate state across multiple independent systems (e.g., Database + Third-Party Gateway + Cloud Storage):
1. **Never perform network I/O inside an open database transaction** (holds row locks and starves connection pool).
2. **Never assume external API calls roll back automatically** when your local transaction aborts.

---

## 6. State Recovery & Self-Healing Architectures

When timeouts and crashes inevitably occur, systems must provide automated mechanisms to detect and repair inconsistent state.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          INCONSISTENT STATE DETECTED                        │
│             (Timeout during checkout / Database-Gateway mismatch)           │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
┌──────────────────────┐    ┌──────────────────────┐    ┌──────────────────────┐
│ CRASH-ONLY IDEMPOTENT│    │   ASYNC SWEEPER /    │    │ COMPENSATING SAGA /  │
│        REPLAY        │    │    RECONCILIATION    │    │    PROBING REPAIR    │
│                      │    │                      │    │                      │
│ Client re-sends with │    │ Cron worker queries  │    │ Saga Coordinator     │
│ same Idempotency-Key;│    │ gateway & DB for     │    │ queries gateway; if  │
│ server returns saved │    │ stranded "PENDING"   │    │ charged, executes    │
│ result without re-op.│    │ states & heals them. │    │ refund compensation. │
└──────────────────────┘    └──────────────────────┘    └──────────────────────┘
```

### 6.1 Crash-Only Idempotent Replay
- Every mutating request must include a deterministic UUIDv4 `Idempotency-Key`.
- Server records an atomic lock on the key:
  - If the key is currently `PROCESSING`, subsequent requests wait or fast-fail with `409 Conflict`.
  - If the key is `COMPLETED`, the server returns the cached response payload immediately without re-executing business logic.
  - If the previous execution failed with a transient timeout, the client can safely replay the request.

### 6.2 Asynchronous Background Reconciliation (The Sweeper Pattern)
For operations left in indeterminate states (`ORDER_STATUS_PENDING`):
1. A background reconciliation daemon runs periodically (e.g., every 60 seconds).
2. It queries for records stranded in `PENDING` older than $5\text{ minutes}$.
3. For each stranded record, it executes a **Status Inquiry Probe** against the downstream payment/shipping provider using the original transaction reference.
4. If the provider processed the transaction, the sweeper marks the local order as `SUCCESS`.
5. If the provider has no record of the transaction, the sweeper marks the order as `FAILED` and releases inventory reservations.

### 6.3 Compensating Sagas with State Probing
Before executing compensating rollbacks ($C_i$):
- Always perform a **read probe** to verify whether the forward action ($T_i$) actually executed.
- If $T_i$ timed out but succeeded downstream, execute compensation ($C_i$, e.g. refund).
- If $T_i$ never reached downstream, mark step as cancelled without triggering unnecessary refund API calls.

---

## 7. Telemetry & Observability: Tracking Timeout Metrics in Code

A resilient architecture must never allow timeouts to expire silently. Every timeout event must be instrumented, categorized, and exported as a first-class operational metric.

### 7.1 Prometheus Metrics Schema for Timeouts

```prometheus
# HELP resilience_timeouts_total Total number of execution timeouts encountered
# TYPE resilience_timeouts_total counter
resilience_timeouts_total{dependency="payment_gateway", operation="charge", timeout_type="attempt"} 14
resilience_timeouts_total{dependency="payment_gateway", operation="charge", timeout_type="overall"} 2

# HELP resilience_timeout_duration_seconds Histogram of elapsed time prior to timeout cancellation
# TYPE resilience_timeout_duration_seconds histogram
resilience_timeout_duration_seconds_bucket{dependency="payment_gateway", operation="charge", le="0.25"} 14
```

### 7.2 Tracking "# of Requests Timed Out" in Go

In the service codebase (`pkg/checkout/telemetry.go`), the `TelemetryRecorder` explicitly tracks timeout counts across two dimensions:
1. **Per-Attempt Socket Timeouts** (`AttemptTimeoutsCount`): Number of individual HTTP/gRPC network attempts aborted due to a connection or socket read deadline.
2. **Overall Operation Timeouts** (`OverallTimeoutsCount`): Number of end-to-end user transactions terminated because cumulative retries exceeded the SLA envelope.

```go
// Integration in Failsafe-Go Policy
timeoutPolicy := timeout.Builder[PaymentResponse](200 * time.Millisecond).
    OnTimeoutExceeded(func(event failsafe.ExecutionEvent[PaymentResponse]) {
        telemetry.RecordTimeout("payment_charge", event.ElapsedAttempts().Milliseconds())
        prometheusTimeoutsTotal.WithLabelValues("payment_gateway", "charge", "attempt").Inc()
    }).
    Build()
```

