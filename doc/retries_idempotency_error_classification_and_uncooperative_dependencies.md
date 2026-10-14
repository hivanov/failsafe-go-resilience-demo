# Retries, Idempotency, Error Classification & Uncooperative Dependencies

This document provides a comprehensive guide to safe retry dynamics, the mathematics of idempotency, deterministic error classification, and architectural strategies for wrapping, patching, and working around uncooperative or poorly behaved third-party dependencies.

---

## Table of Contents

- [1. The Retry Dilemma: Self-Healing vs. Self-Destruction](#1-the-retry-dilemma-self-healing-vs-self-destruction)
  - [1.1 When Retries Help: Transient Physical Glitches](#11-when-retries-help-transient-physical-glitches)
  - [1.2 When Retries Destroy: Deterministic Rejections & Amplification](#12-when-retries-destroy-deterministic-rejections--amplification)
- [2. The Fundamental Prerequisite: Strict Idempotency](#2-the-fundamental-prerequisite-strict-idempotency)
  - [2.1 Mathematical Definition of Idempotent Operations](#21-mathematical-definition-of-idempotent-operations)
  - [2.2 The Anatomy of an Idempotency Key Pipeline](#22-the-anatomy-of-an-idempotency-key-pipeline)
  - [2.3 Canonical Payload Fingerprinting (SHA-256 Hashing)](#23-canonical-payload-fingerprinting-sha-256-hashing)
  - [2.4 Idiomatic Go Implementation: Database-Backed Idempotency Guard](#24-idiomatic-go-implementation-database-backed-idempotency-guard)
- [3. Error Classification Taxonomy: Retriable vs. Non-Retriable](#3-error-classification-taxonomy-retriable-vs-non-retriable)
  - [3.1 The 3-Tier Classification Matrix](#31-the-3-tier-classification-matrix)
  - [3.2 HTTP Status Code Mapping Reference](#32-http-status-code-mapping-reference)
  - [3.3 gRPC Canonical Code Mapping Reference](#33-grpc-canonical-code-mapping-reference)
- [4. The Critical Role of Descriptive Error Labeling (RFC 7807)](#4-the-critical-role-of-descriptive-error-labeling-rfc-7807)
  - [4.1 Why Generic Errors Cripple Client Policies](#41-why-generic-errors-cripple-client-policies)
  - [4.2 Implementing RFC 7807 Problem Details in Go](#42-implementing-rfc-7807-problem-details-in-go)
- [5. Working Around Uncooperative Dependencies: 5 Patching Strategies](#5-working-around-uncooperative-dependencies-5-patching-strategies)
  - [5.1 Strategy 1: The Opaque 500 Disambiguation Layer (Body Heuristics)](#51-strategy-1-the-opaque-500-disambiguation-layer-body-heuristics)
  - [5.2 Strategy 2: Synthetic Probing & State Interrogation](#52-strategy-2-synthetic-probing--state-interrogation)
  - [5.3 Strategy 3: Client-Side Translation Decorator (Error Normalization)](#53-strategy-3-client-side-translation-decorator-error-normalization)
  - [5.4 Strategy 4: Shadow Validation & Pre-Flight Verification](#54-strategy-4-shadow-validation--pre-flight-verification)
  - [5.5 Strategy 5: Optimistic Single-Attempt Barrier with Asynchronous Repair](#55-strategy-5-optimistic-single-attempt-barrier-with-asynchronous-repair)

---

## 1. The Retry Dilemma: Self-Healing vs. Self-Destruction

Retries are the most intuitive resilience mechanism, but also the most dangerous. Improperly configured retries are the primary driver of catastrophic cascading outages in cloud systems.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            THE RETRY DECISION TREE                          │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                         Is the operation IDEMPOTENT?
                                       │
                      ┌────────────────┴────────────────┐
                     YES                               NO
                      │                                 │
             Is the error TRANSIENT?         [ RETRIES FORBIDDEN ]
                      │                      Risk: Double-charge,
             ┌────────┴────────┐             Duplicate records,
            YES                NO            Corrupted state.
             │                 │
    [ RETRY WITH JITTER ]  [ FAST-FAIL (NO RETRY) ]
    Exponential Backoff    Return error to caller
    + Per-Attempt Timeout  immediately (< 1ms).
```

### 1.1 When Retries Help: Transient Physical Glitches
Retries succeed only against **transient, self-correcting faults**:
- Momentary TCP packet loss / WiFi handoff drops.
- Downstream server rolling restart / brief pod reschedule (100–300ms window).
- Ephemeral database connection reset (`connection reset by peer`).
- HTTP 429 Rate Limit (when honoring the downstream `Retry-After` header).
- HTTP 503 Service Unavailable during blue-green deployment switches.

### 1.2 When Retries Destroy: Deterministic Rejections & Amplification
Retrying against deterministic faults or overloaded dependencies causes fatal system damage:
1. **The Retry Storm (Amplification Attack)**: If 1,000 clients send requests to a downstream dependency operating at 100% capacity, and each client retries 3 times, the incoming load instantly spikes to **4,000 RPS**, driving the dependency into complete CPU/memory lockup.
2. **Deterministic Rejections**: Retrying a `400 Bad Request`, `401 Unauthorized`, `422 Unprocessable Entity`, or `InvalidCreditCardNumber` will **never succeed**. It wastes network sockets, burns CPU, inflates caller latency, and risks user account lockouts.

---

## 2. The Fundamental Prerequisite: Strict Idempotency

### 2.1 Mathematical Definition of Idempotent Operations
An operation $f(x)$ is idempotent if and only if executing it once has the exact same side-effects as executing it $N$ times:
$$f(f(x)) = f(x) \quad \forall N \ge 1$$

In distributed systems, an API is idempotent if sending identical requests repeatedly produces identical server-side state transitions and identical return payloads.

### 2.2 The Anatomy of an Idempotency Key Pipeline

```
CLIENT                              GATEWAY / SERVER                      DATABASE
  │                                        │                                 │
  ├── POST /v1/orders ────────────────────>│                                 │
  │   Idempotency-Key: "uuid-123"          │                                 │
  │   Body: {"sku": "A1", "qty": 1}        ├── SELECT FROM idempotency_keys ─>│
  │                                        │   WHERE key = 'uuid-123'        │
  │                                        │<── NOT FOUND ───────────────────┤
  │                                        │                                 │
  │                                        ├── INSERT INTO idempotency_keys ─>│
  │                                        │   (key, status, req_hash)       │
  │                                        │   VALUES ('uuid-123', 'LOCKED') │
  │                                        │                                 │
  │                                        ├── EXECUTE BUSINESS LOGIC        │
  │                                        │   (Reserve Inventory, Charge)   │
  │                                        │                                 │
  │                                        ├── UPDATE idempotency_keys ─────>│
  │                                        │   SET status = 'RESOLVED',      │
  │                                        │       response_body = '...'     │
  │<── 200 OK {"order_id": "ord_999"} ─────┤                                 │
```

### 2.3 Canonical Payload Fingerprinting (SHA-256 Hashing)
To prevent malicious or buggy clients from reusing the same `Idempotency-Key` with different request payloads:
1. Normalize JSON request bytes (sort dictionary keys, strip trailing whitespace).
2. Calculate the SHA-256 hash of the canonical request body:
   $$\text{PayloadHash} = \text{SHA256}(\text{Canonicalize}(\text{RequestBytes}))$$
3. Store `PayloadHash` alongside the idempotency key in the database.
4. If a subsequent request arrives with matching `Idempotency-Key` but differing `PayloadHash`, abort immediately with `422 Unprocessable Entity` ("Idempotency Key Payload Mismatch").

### 2.4 Idiomatic Go Implementation: Database-Backed Idempotency Guard

```go
package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrRequestInProgress = errors.New("concurrent request with identical idempotency key in progress")
	ErrPayloadMismatch   = errors.New("idempotency key reused with differing request payload")
)

type Guard struct {
	db *sql.DB
}

func NewGuard(db *sql.DB) *Guard {
	return &Guard{db: db}
}

func HashPayload(v any) (string, error) {
	bytes, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:]), nil
}

// ExecuteProtected wraps business logic with atomic database idempotency guarantees.
func (g *Guard) ExecuteProtected(ctx context.Context, key string, payload any, fn func() ([]byte, error)) ([]byte, error) {
	payloadHash, err := HashPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to hash payload: %w", err)
	}

	// 1. Attempt to insert the lock record
	query := `
		INSERT INTO idempotency_ledger (key, payload_hash, status, created_at, expires_at)
		VALUES ($1, $2, 'PROCESSING', NOW(), NOW() + INTERVAL '24 hours')
		ON CONFLICT (key) DO NOTHING;
	`
	res, err := g.db.ExecContext(ctx, query, key, payloadHash)
	if err != nil {
		return nil, err
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Key already exists: check status and payload hash
		var storedHash, status string
		var cachedResp []byte
		err := g.db.QueryRowContext(ctx, "SELECT payload_hash, status, response_body FROM idempotency_ledger WHERE key = $1", key).
			Scan(&storedHash, &status, &cachedResp)
		if err != nil {
			return nil, err
		}

		if storedHash != payloadHash {
			return nil, ErrPayloadMismatch
		}
		if status == "PROCESSING" {
			return nil, ErrRequestInProgress
		}
		// Return previously computed result without re-executing business logic
		return cachedResp, nil
	}

	// 2. We acquired the lock: execute actual business logic
	respBytes, execErr := fn()
	if execErr != nil {
		// Delete key on transient failure to permit clean retry
		_, _ = g.db.ExecContext(context.Background(), "DELETE FROM idempotency_ledger WHERE key = $1", key)
		return nil, execErr
	}

	// 3. Store resolved response body and mark COMPLETED
	_, _ = g.db.ExecContext(context.Background(), `
		UPDATE idempotency_ledger 
		SET status = 'COMPLETED', response_body = $2 
		WHERE key = $1;
	`, key, respBytes)

	return respBytes, nil
}
```

---

## 3. Error Classification Taxonomy: Retriable vs. Non-Retriable

### 3.1 The 3-Tier Classification Matrix

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           ERROR CLASSIFICATION TIER                         │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
┌──────────────────────┐    ┌──────────────────────┐    ┌──────────────────────┐
│  TIER 1: RETRIABLE   │    │ TIER 2: DETERMINISTIC│    │   TIER 3: FATAL /    │
│     (TRANSIENT)      │    │    NON-RETRIABLE     │    │     AMBIGUOUS        │
│                      │    │                      │    │                      │
│ • TCP Connection Drop│    │ • 400 Bad Request    │    │ • 500 Generic Error  │
│ • HTTP 503 / 429     │    │ • 401 Unauthorized   │    │ • 504 Timeout on     │
│ • Context Deadline   │    │ • 403 Forbidden      │    │   non-idempotent API │
│ • SQL Deadlock (40P01│    │ • 404 Not Found      │    │ • Truncated Body /   │
│   with backoff)      │    │ • 422 Validation Err │    │   Malformed JSON     │
│                      │    │                      │    │                      │
│ ACTION: Retry with   │    │ ACTION: Fast-Fail    │    │ ACTION: Do NOT retry │
│ Exponential Jitter.  │    │ immediately (< 1ms). │    │ blindly; Probe/Heal. │
└──────────────────────┘    └──────────────────────┘    └──────────────────────┘
```

### 3.2 HTTP Status Code Mapping Reference

| HTTP Code | Classification | Retry Policy Action | Architectural Rationale |
| :--- | :--- | :--- | :--- |
| **`400 Bad Request`** | Deterministic | **NEVER RETRY** | Malformed syntax or invalid parameter; retry will always fail. |
| **`401 Unauthorized`** | Deterministic | **NEVER RETRY** (Direct) | Missing or invalid auth header. (Retry only after refreshing OAuth token). |
| **`403 Forbidden`** | Deterministic | **NEVER RETRY** | Caller lacks permissions; retry will always fail. |
| **`404 Not Found`** | Deterministic | **NEVER RETRY** | Entity does not exist; retrying wastes downstream lookup resources. |
| **`409 Conflict`** | Conditional | **RETRY WITH CAUTION** | OCC version collision or lock contention; retry only with backoff. |
| **`422 Unprocessable`**| Deterministic | **NEVER RETRY** | Semantic validation error (e.g. invalid credit card format). |
| **`429 Too Many Reqs`**| Transient | **RETRY (WITH JITTER)** | Rate limited; honor `Retry-After` header or apply exponential backoff. |
| **`500 Internal Error`**| Ambiguous | **HANDLE VIA ADAPTER**| May be unhandled null pointer or downstream crash. Requires body inspection. |
| **`502 Bad Gateway`** | Transient | **RETRY (WITH JITTER)** | Upstream proxy lost backend node; retry connects to alternative replica. |
| **`503 Unavailable`** | Transient | **RETRY (WITH JITTER)** | Service temporarily overloaded or deploying; prime candidate for retry. |
| **`504 Gateway Timeout`| Ambiguous | **RETRY ONLY IF IDEMPOTENT** | Downstream timed out; request may have partially committed. |

### 3.3 gRPC Canonical Code Mapping Reference

| gRPC Status Code | Classification | Recommended Failsafe-Go Policy |
| :--- | :--- | :--- |
| `Codes.OK` (0) | Success | Pass result through. |
| `Codes.InvalidArgument` (3) | Deterministic | Fast-fail immediately. |
| `Codes.DeadlineExceeded` (4)| Transient / Ambiguous | Retry with attempt timeout if idempotent. |
| `Codes.NotFound` (5) | Deterministic | Fast-fail immediately. |
| `Codes.AlreadyExists` (6) | Deterministic | Fast-fail / interpret as idempotent success. |
| `Codes.PermissionDenied` (7)| Deterministic | Fast-fail immediately. |
| `Codes.ResourceExhausted` (8)| Transient | Retry with backoff or shed load. |
| `Codes.Aborted` (10) | Transient | Retry (concurrency conflict / lock abort). |
| `Codes.Unavailable` (14) | Transient | Standard retry with exponential backoff & jitter. |

---

## 4. The Critical Role of Descriptive Error Labeling (RFC 7807)

### 4.1 Why Generic Errors Cripple Client Policies
When a service returns generic, uninformative errors like `{"error": "something went wrong"}` with HTTP 500:
1. The calling client **cannot distinguish** between a transient database connection hiccup and a permanent business rule failure (e.g., account suspended).
2. The client is forced to make a dangerous binary choice: either retry blindly (risking server collapse) or fail fast (degrading availability).

### 4.2 Implementing RFC 7807 Problem Details in Go
RFC 7807 defines a standardized, machine-readable JSON format for HTTP error responses:

```go
package errors

import (
	"encoding/json"
	"net/http"
)

// ProblemDetails represents an RFC 7807 structured error payload.
type ProblemDetails struct {
	Type          string            `json:"type"`                     // URI reference identifying problem type
	Title         string            `json:"title"`                    // Short, human-readable summary
	Status        int               `json:"status"`                   // HTTP status code
	Detail        string            `json:"detail,omitempty"`         // Human-readable explanation specific to occurrence
	Instance      string            `json:"instance,omitempty"`       // URI reference identifying specific occurrence
	ErrorCode     string            `json:"error_code"`               // Machine-readable business error string
	Retryable     bool              `json:"retryable"`                // Explicit hint for upstream failsafe policies
	RetryAfterSec int               `json:"retry_after_sec,omitempty"`// Recommended delay before retry
	InvalidParams map[string]string `json:"invalid_params,omitempty"` // Validation breakdown if applicable
}

func WriteProblemDetails(w http.ResponseWriter, p ProblemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
```

Example JSON wire response:
```json
{
  "type": "https://api.example.com/errors/inventory-locked",
  "title": "Inventory Row Lock Contention",
  "status": 503,
  "detail": "SKU item 9912 is currently locked by a concurrent checkout process.",
  "instance": "/v1/orders/ord_8812/items/9912",
  "error_code": "ERR_INVENTORY_ROW_LOCK_TIMEOUT",
  "retryable": true,
  "retry_after_sec": 1
}
```

---

## 5. Working Around Uncooperative Dependencies: 5 Patching Strategies

In enterprise environments, upstream services and third-party legacy APIs often fail to adhere to RFC 7807, returning opaque HTTP 500s, truncated payloads, or non-standard error strings.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│               UNCOOPERATIVE DEPENDENCY ADAPTER PIPELINE                     │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                      Raw HTTP Response / Error Code
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CLIENT-SIDE TRANSLATION DECORATOR                        │
│                                                                             │
│  [ Step 1: Status & Header Analysis ] ──────────────────────────────────┐   │
│  ├── Is status 200 OK with `{"status": "ERROR"}` in body?               │   │
│  ├── Is status 500 containing string "Record not found"?                │   │
│                                                                         ▼   │
│  [ Step 2: Body Heuristic & Regex Parser ] ─────────────────────────────┤   │
│  ├── Extracts legacy vendor error code (e.g. "ERR_CC_EXPIRED")          │   │
│  └── Maps vendor error to Standard Typed Domain Error                   │   │
│                                                                         ▼   │
│  [ Step 3: Failsafe-Go Policy Execution ] ──────────────────────────────┤   │
│  ├── If Normalized Error is TRANSIENT ──> Allow Retry                   │   │
│  └── If Normalized Error is DETERMINISTIC ──> Fast-Fail Immediately     │   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.1 Strategy 1: The Opaque 500 Disambiguation Layer (Body Heuristics)
Many legacy frameworks return `HTTP 500 Internal Server Error` for SQL constraint violations or user validation issues.
- **Remediation**: Inspect the response body using bounded string matching / regex before bubbling up the error.
- If body contains `"DuplicateKeyException"` or `"UserNotFound"`, translate HTTP status to a deterministic `ErrEntityNotFound` or `ErrConflict`, preventing unneeded retries.

### 5.2 Strategy 2: Synthetic Probing & State Interrogation
When a downstream write operation times out with an ambiguous error:
- Do not retry the write immediately.
- Execute an out-of-band **Inquiry Query** (e.g. `GET /v1/payments/by-reference/{idempotency_key}`) to verify if the previous attempt succeeded.
- If the probe returns `FOUND_SUCCESS`, treat the timed-out attempt as successful and return the saved payload.

### 5.3 Strategy 3: Client-Side Translation Decorator (Error Normalization)
Wrap the raw downstream SDK client in a **Translation Decorator** that intercepts raw HTTP responses and maps them into strongly typed domain errors before presenting them to the `failsafe-go` retry policy.

```go
type VendorAdapterClient struct {
    rawClient RawLegacyAPIClient
}

func (a *VendorAdapterClient) Execute(ctx context.Context, req Request) (Response, error) {
    resp, rawErr := a.rawClient.Call(ctx, req)
    if rawErr != nil {
        return Response{}, NormalizeNetworkError(rawErr)
    }
    
    // Catch the "Silent Failure" Anti-Pattern: 200 OK with internal error payload
    if resp.StatusCode == 200 && resp.RawVendorStatus == "FAILED_CARD_DECLINED" {
        return Response{}, ErrDeterministicPaymentDeclined // NON-RETRIABLE
    }
    if resp.StatusCode == 500 && resp.RawVendorStatus == "DB_CONNECTION_TIMEOUT" {
        return Response{}, ErrTransientDependencyUnavailable // RETRIABLE
    }
    return resp, nil
}
```

### 5.4 Strategy 4: Shadow Validation & Pre-Flight Verification
If a downstream service has brittle validation that crashes with 500s on bad inputs:
- Implement client-side **Shadow Validation** before calling the downstream client.
- Validate email formats, phone numbers, tax IDs, and credit card Luhn algorithms locally.
- Reject invalid requests at your service boundary before invoking the uncooperative dependency.

### 5.5 Strategy 5: Optimistic Single-Attempt Barrier with Asynchronous Repair
If an external API is notoriously non-idempotent and prone to double-charging on retries:
- Set `RetryPolicy.MaxAttempts(1)` (disable retries on the synchronous request path entirely).
- If the call fails or times out, place the transaction in a durable queue (`PENDING_RECONCILIATION`).
- Allow an asynchronous background worker with dedicated reconciliation logic to resolve the transaction safely.

---

## 6. Telemetry & Metrics: Monitoring Retries & Retry Amplification

Tracking retries requires distinguishing between the **total volume of retries** across the system and the **retry attempt index distribution for a specific operation**.

### 6.1 Prometheus Metrics Schema for Retries

```prometheus
# HELP resilience_retries_total Total retry attempts executed
# TYPE resilience_retries_total counter
resilience_retries_total{dependency="payment_gateway", operation="charge", attempt="1"} 42
resilience_retries_total{dependency="payment_gateway", operation="charge", attempt="2"} 5
resilience_retries_total{dependency="payment_gateway", operation="charge", attempt="3"} 1

# HELP resilience_retry_exhaustions_total Executions where all retries failed
# TYPE resilience_retry_exhaustions_total counter
resilience_retry_exhaustions_total{dependency="payment_gateway", operation="charge"} 1
```

### 6.2 Tracking Retries in Go (`pkg/checkout/telemetry.go`)

In the Go implementation:
- `TotalRetriesCount`: Monotonic counter tracking all retry attempts across all dependencies.
- `RetriesByOperation`: Counter map tracking retries per operation (e.g. `payment_charge` vs `inventory_lock`).
- `RetriesByOperationAndAttempt`: 2D map tracking the distribution of attempts ($N=1, N=2, N=3$). If attempt #2 retries are high but attempt #3 is near-zero, a 2-attempt budget is optimal. If attempt #3 retries frequently fail, the 3rd attempt represents wasted compute.

```go
// Integration in Failsafe-Go Policy
retryPolicy := retrypolicy.Builder[PaymentResponse]().
    HandleErrors(ErrTransientGateway503, ErrTCPConnectionReset).
    WithBackoff(50*time.Millisecond, 500*time.Millisecond).
    WithJitter(0.2).
    WithMaxAttempts(3).
    OnRetry(func(event failsafe.ExecutionEvent[PaymentResponse]) {
        telemetry.RecordRetry("payment_charge", event.Attempts(), event.LastError())
        prometheusRetriesTotal.WithLabelValues("payment_gateway", "charge", fmt.Sprintf("%d", event.Attempts())).Inc()
    }).
    Build()
```

