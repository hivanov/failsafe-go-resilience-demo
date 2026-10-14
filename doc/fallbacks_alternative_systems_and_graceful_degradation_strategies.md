# Fallbacks, Alternative Systems, Heuristic Logics & Graceful Degradation

This document provides a comprehensive architectural guide to fallback strategies beyond basic caching, covering multi-provider failover, alternative heuristic logics, degraded default behaviors, optimistic acceptance, and business risk containment in distributed systems.

---

## Table of Contents

- [1. Expanding Beyond Caching: The Multi-Dimensional Fallback Spectrum](#1-expanding-beyond-caching-the-multi-dimensional-fallback-spectrum)
- [2. Category 1: Alternative Systems & Multi-Vendor Redundancy](#2-category-1-alternative-systems--multi-vendor-redundancy)
  - [2.1 Secondary Payment Gateway Failover (Stripe $\rightarrow$ Adyen)](#21-secondary-payment-gateway-failover-stripe-rightarrow-adyen)
  - [2.2 Multi-Vendor Communication Routing (Twilio $\rightarrow$ Infobip / AWS SNS)](#22-multi-vendor-communication-routing-twilio-rightarrow-infobip--aws-sns)
  - [2.3 Multi-Region & Cloud-Failover Topologies (Active-Active vs. Hot-Standby)](#23-multi-region--cloud-failover-topologies-active-active-vs-hot-standby)
- [3. Category 2: Alternative Logics & Heuristic Simplification](#3-category-2-alternative-logics--heuristic-simplification)
  - [3.1 ML Model Outages: Degrading to Static Rule Engines](#31-ml-model-outages-degrading-to-static-rule-engines)
  - [3.2 Live Foreign Exchange (FX) Conversion vs. Bounded Static Tables](#32-live-foreign-exchange-fx-conversion-vs-bounded-static-tables)
  - [3.3 Fail-Open vs. Fail-Closed Security Policy Design](#33-fail-open-vs-fail-closed-security-policy-design)
- [4. Category 3: Alternative Default Behaviors & Asynchronous Degradation](#4-category-3-alternative-default-behaviors--asynchronous-degradation)
  - [4.1 The "Review Pending" Pattern (Deferring Decision to Asynchronous Triage)](#41-the-review-pending-pattern-deferring-decision-to-asynchronous-triage)
  - [4.2 Soft Entitlement Grants (Temporary Access During Outages)](#42-soft-entitlement-grants-temporary-access-during-outages)
  - [4.3 Optimistic Acceptance with Post-Hoc Financial Reconciliation](#43-optimistic-acceptance-with-post-hoc-financial-reconciliation)
- [5. The Resilience-Risk Trade-Off Matrix](#5-the-resilience-risk-trade-off-matrix)
- [6. Complete Go Implementation: Composite Decorator with Multi-Tier Fallback Strategies](#6-complete-go-implementation-composite-decorator-with-multi-tier-fallback-strategies)

---

## 1. Expanding Beyond Caching: The Multi-Dimensional Fallback Spectrum

In naive resilience implementations, "fallback" is often treated as synonymous with reading stale data from a Redis or CDN cache. However, for mutating operations (payments, fraud checks, orders, identity verification), reading from a cache is mathematically impossible.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       THE 4 PILLARS OF FALLBACK DESIGN                      │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
        ┌──────────────────┬───────────┴──────────┬──────────────────┐
        ▼                  ▼                      ▼                  ▼
┌───────────────┐  ┌───────────────┐      ┌───────────────┐  ┌───────────────┐
│   CACHING     │  │  ALTERNATIVE  │      │  ALTERNATIVE  │  │   DEGRADED    │
│  (Read-Path)  │  │    SYSTEMS    │      │    LOGICS     │  │   BEHAVIOR    │
│               │  │  (Multi-Peer) │      │  (Heuristics) │  │  (Async / UX) │
│ • Edge CDN    │  │ • Backup      │      │ • Static Rule │  │ • "Review     │
│ • Local Cache │  │   Gateways    │      │   Engines     │  │   Pending"    │
│ • Stale Redis │  │ • Secondary   │      │ • Static Rate │  │ • Soft Grants │
│ • Singleflight│  │   SMS Vendors │      │   Tables      │  │ • Optimistic  │
│   Dedupe      │  │ • Hot Standby │      │ • Fail-Open   │  │   Acceptance  │
└───────────────┘  └───────────────┘      └───────────────┘  └───────────────┘
```

---

## 2. Category 1: Alternative Systems & Multi-Vendor Redundancy

When a critical third-party vendor suffers a total infrastructure outage, the only viable fallback is transparent runtime failover to a completely independent secondary vendor.

### 2.1 Secondary Payment Gateway Failover (Stripe $\rightarrow$ Adyen)
If the primary payment processor returns 500/503 or trips its circuit breaker:
1. Intercept the error in the `failsafe-go` fallback handler.
2. Translate the payment payload into the secondary provider's API format.
3. Propagate the **same idempotency key** with a vendor prefix (e.g., `idemp_adyen_12345`) to prevent duplicate transactions.
4. Record multi-gateway telemetry for merchant reconciliation.

```
                    ┌───────────────────────────────┐
                    │       CHECKOUT INGRESS        │
                    └───────────────┬───────────────┘
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │    PRIMARY GATEWAY (Stripe)   │
                    └───────────────┬───────────────┘
                                    │
                      [ 503 Outage / Breaker OPEN ]
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │   SECONDARY GATEWAY (Adyen)   │
                    └───────────────┬───────────────┘
                                    │
                        [ 200 OK Authorized ]
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │      ORDER SUCCESSFUL         │
                    └───────────────────────────────┘
```

### 2.2 Multi-Vendor Communication Routing (Twilio $\rightarrow$ Infobip / AWS SNS)
For mission-critical Two-Factor Authentication (2FA) SMS delivery:
- Primary vendor (Twilio) timeout threshold: $1500\text{ms}$.
- Fallback: Dispatch via secondary vendor (Infobip / AWS SNS).
- Fallback 2: Offer user alternative channel (WhatsApp or Email OTP).

### 2.3 Multi-Region & Cloud-Failover Topologies (Active-Active vs. Hot-Standby)
- **Active-Active Cross-Region**: Route traffic between AWS us-east-1 and AWS eu-central-1 via Anycast DNS (Route53 / Cloudflare).
- **Hybrid Cloud Warm-Standby**: On-premise private cloud failover to public cloud during datacenter fiber cuts.

---

## 3. Category 2: Alternative Logics & Heuristic Simplification

When a complex, compute-heavy, or AI/ML-driven dependency degrades, the service can fall back to a deterministic, lightweight algorithmic heuristic.

### 3.1 ML Model Outages: Degrading to Static Rule Engines
- **Primary Path**: Deep Neural Network Fraud Detection microservice calculates a real-time risk score ($0.00–1.00$) in $80\text{ms}$.
- **Fallback Path**: When the ML service times out or trips its circuit breaker, fall back to an **In-Memory Heuristic Rule Engine**:
  * Rule 1: Is transaction amount $> \$2,000$? $\rightarrow$ Flag for manual review.
  * Rule 2: Is IP country different from billing country? $\rightarrow$ Request 3D-Secure 2FA.
  * Rule 3: Is account age $> 1\text{ year}$ with zero chargebacks? $\rightarrow$ **Approve immediately**.

### 3.2 Live Foreign Exchange (FX) Conversion vs. Bounded Static Tables
- **Primary Path**: Query live FX market API for exact microsecond exchange rates.
- **Fallback Path**: Read rates from a daily static lookup table stored in application memory, applying a conservative $1.5\%$ volatility buffer to protect business margin.

### 3.3 Fail-Open vs. Fail-Closed Security Policy Design

| Dimension | Fail-Open Policy (Optimistic) | Fail-Closed Policy (Pessimistic) |
| :--- | :--- | :--- |
| **Definition** | If authorization service times out, **grant access**. | If authorization service times out, **deny access**. |
| **Use Case** | Read-only content catalog, social media likes, premium video playback. | Bank money transfers, admin portal logins, nuclear/medical controls. |
| **Business Impact** | Minor potential revenue leakage; zero user friction. | Absolute security; potential customer outage during auth downtime. |

---

## 4. Category 3: Alternative Default Behaviors & Asynchronous Degradation

### 4.1 The "Review Pending" Pattern (Deferring Decision to Asynchronous Triage)
Instead of returning a hard HTTP 500 error to a purchasing customer when the risk engine fails:
1. Complete the database transaction with state `ORDER_STATUS_REVIEW_PENDING`.
2. Return HTTP `202 Accepted` to the client:
   ```json
   {
     "order_id": "ord_8841",
     "status": "REVIEW_PENDING",
     "message": "Your order is being verified and will be confirmed via email within 30 minutes."
   }
   ```
3. Push an audit task to an asynchronous worker queue (Kafka / Solace) for offline re-evaluation or human agent triage.

### 4.2 Soft Entitlement Grants (Temporary Access During Outages)
If a subscription verification service is down when a user launches a software application:
- Do **not** lock the user out with an error screen.
- Grant a temporary **72-Hour Grace Period Token** (`SoftEntitlementGrant`).
- Log the soft grant and verify subscription status in the background when connectivity returns.

### 4.3 Optimistic Acceptance with Post-Hoc Financial Reconciliation
In high-volume transit systems (metro turnstiles) or offline point-of-sale terminals:
- If terminal cannot reach central banking server in $< 200\text{ms}$, open the gate optimistically.
- Batch transaction locally and settle funds when network connectivity is restored.
- Accept a predictable $< 0.05\%$ bad-debt loss in exchange for $100\%$ passenger throughput.

---

## 5. The Resilience-Risk Trade-Off Matrix

```
HIGH RISK  ┌────────────────────────────────────────────────────────┐
           │                                      FAIL-CLOSED       │
           │                                   (Reject on Error)    │
           │                                                        │
           │                  HEURISTIC LOGIC                       │
           │               (Rule-based checks)                      │
           │                                                        │
           │   FAILOVER SYSTEM                                      │
           │   (Secondary Vendor)                                   │
           │                                                        │
           │                                      FAIL-OPEN         │
           │                                   (Grant on Error)     │
LOW RISK   └────────────────────────────────────────────────────────┘
           LOW FINANCIAL IMPACT                   HIGH FINANCIAL IMPACT
```

---

## 6. Complete Go Implementation: Composite Decorator with Multi-Tier Fallback Strategies

```go
package fallback

import (
	"context"
	"errors"
	"log"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

type PaymentRequest struct {
	OrderID        string
	AmountCents    int64
	Currency       string
	IdempotencyKey string
	UserAccountAge int // In days
}

type PaymentResult struct {
	TransactionID string
	Status        string // SUCCESS, REVIEW_PENDING, DEGRADED_ACCEPTED
	GatewayUsed   string
	IsDegraded    bool
}

type PaymentService interface {
	ProcessPayment(ctx context.Context, req PaymentRequest) (PaymentResult, error)
}

type ResilientPaymentService struct {
	primaryGateway   PaymentService
	secondaryGateway PaymentService
	executor         failsafe.Executor[PaymentResult]
}

func NewResilientPaymentService(primary, secondary PaymentService) *ResilientPaymentService {
	// Fallback Strategy:
	// If Primary fails, attempt Secondary Gateway.
	// If Secondary ALSO fails, apply Heuristic Degraded Acceptance ("REVIEW_PENDING").
	fbPolicy := fallback.BuilderWithFunc[PaymentResult](func(exec failsafe.Execution[PaymentResult]) (PaymentResult, error) {
		req := exec.Context().Value("payment_req").(PaymentRequest)
		log.Printf("Primary and Secondary gateways unavailable. Evaluating Heuristic Fallback for Order %s", req.OrderID)

		// Alternative Logic: Rule-based risk acceptance
		if req.AmountCents < 5000 && req.UserAccountAge > 90 {
			// Low-value transaction for trusted user: Optimistic Acceptance!
			return PaymentResult{
				TransactionID: "opt_" + req.OrderID,
				Status:        "DEGRADED_ACCEPTED",
				GatewayUsed:   "OPTIMISTIC_OFFLINE_BUFFER",
				IsDegraded:    true,
			}, nil
		}

		// Higher risk: Defer to Asynchronous Manual Review Queue
		return PaymentResult{
			TransactionID: "rev_" + req.OrderID,
			Status:        "REVIEW_PENDING",
			GatewayUsed:   "ASYNC_REVIEW_QUEUE",
			IsDegraded:    true,
		}, nil
	}).Build()

	retry := retrypolicy.Builder[PaymentResult]().
		WithMaxAttempts(2).
		Build()

	exec := failsafe.NewExecutor[PaymentResult](fbPolicy, retry)

	return &ResilientPaymentService{
		primaryGateway:   primary,
		secondaryGateway: secondary,
		executor:         exec,
	}
}

func (s *ResilientPaymentService) ProcessPayment(ctx context.Context, req PaymentRequest) (PaymentResult, error) {
	ctxWithReq := context.WithValue(ctx, "payment_req", req)

	return s.executor.WithContext(ctxWithReq).GetWithExecution(func(exec failsafe.Execution[PaymentResult]) (PaymentResult, error) {
		// Attempt Primary Gateway on Try 1
		if exec.Attempts() == 0 {
			return s.primaryGateway.ProcessPayment(exec.Context(), req)
		}
		// Attempt Secondary Gateway on Retry
		log.Printf("Failing over to Secondary Gateway (Adyen) for Order %s", req.OrderID)
		return s.secondaryGateway.ProcessPayment(exec.Context(), req)
	})
}

---

## 7. Telemetry & Metrics: Monitoring Fallback Activations

A fallback should never execute silently. An elevated rate of fallback activations indicates that primary business capabilities are degraded, even if the user experiences HTTP 200/202 success.

### 7.1 Prometheus Metrics Schema for Fallbacks

```prometheus
# HELP resilience_fallback_executions_total Total requests served via a fallback strategy
# TYPE resilience_fallback_executions_total counter
resilience_fallback_executions_total{dependency="payment_gateway", strategy="secondary_vendor", fallback_type="adyen"} 48
resilience_fallback_executions_total{dependency="fraud_engine", strategy="heuristic_rules", fallback_type="static_rules"} 112
resilience_fallback_executions_total{dependency="payment_gateway", strategy="degraded_behavior", fallback_type="review_pending"} 15

# HELP resilience_fallback_activation_ratio Percentage of total traffic served by fallbacks
# TYPE resilience_fallback_activation_ratio gauge
resilience_fallback_activation_ratio{dependency="fraud_engine"} 4.8
```

### 7.2 Tracking Fallbacks in Go (`pkg/checkout/telemetry.go`)

In the Go implementation:
- `FallbackRunsCount`: Monotonic counter tracking all fallback executions across the application.
- `FallbacksByOperation`: Counter map tracking fallback activations per operation (e.g. `fraud_evaluation` vs `payment_charge`).

```go
// Integration in Failsafe-Go Policy
fbPolicy := fallback.BuilderWithFunc[PaymentResult](func(exec failsafe.Execution[PaymentResult]) (PaymentResult, error) {
    telemetry.RecordFallback("payment_charge", "primary_and_secondary_gateways_failed")
    prometheusFallbackTotal.WithLabelValues("payment_gateway", "degraded_behavior", "review_pending").Inc()
    return DegradedPendingResult, nil
}).Build()
```

```
