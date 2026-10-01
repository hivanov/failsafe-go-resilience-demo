// Package policies provides resilient decorator implementations and failsafe-go policy
// configurations for downstream payment, inventory, fraud, and loyalty services.
package policies

import (
	"context"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// PaymentPolicyConfig holds configuration parameters for the multi-tier payment execution policy onion.
type PaymentPolicyConfig struct {
	// OverallOperationTimeout establishes the hard upper bound across all retry attempts combined.
	OverallOperationTimeout time.Duration

	// AttemptTimeout establishes the socket timeout for each individual network attempt.
	AttemptTimeout time.Duration

	// MaxRetries specifies the maximum number of retry attempts following an initial failure.
	MaxRetries int

	// BackoffMin is the minimum delay before the first retry attempt.
	BackoffMin time.Duration

	// BackoffMax is the maximum ceiling delay for exponential backoff.
	BackoffMax time.Duration

	// JitterFactor adds randomized variance (0.0 to 1.0) to backoff intervals to prevent thundering herds.
	JitterFactor float64

	// CBThreshold is the number of failure executions needed to trip the circuit breaker open.
	CBThreshold uint

	// CBCapacity is the rolling execution window size for failure ratio calculation.
	CBCapacity uint

	// CBDelay is the cooldown period spent in OPEN state before transitioning to HALF_OPEN.
	CBDelay time.Duration

	// Telemetry receives policy lifecycle events for metrics and tracing.
	Telemetry checkout.TelemetryRecorder
}

// DefaultPaymentPolicyConfig returns production-calibrated defaults derived from the 800ms business SLA.
// Allocates a 400ms overall operation timeout, 150ms per-attempt timeout, up to 2 retries with jitter,
// and a 3/10 failure ratio circuit breaker.
func DefaultPaymentPolicyConfig(telemetry checkout.TelemetryRecorder) PaymentPolicyConfig {
	return PaymentPolicyConfig{
		OverallOperationTimeout: 400 * time.Millisecond,
		AttemptTimeout:          150 * time.Millisecond,
		MaxRetries:              2,
		BackoffMin:              40 * time.Millisecond,
		BackoffMax:              120 * time.Millisecond,
		JitterFactor:            0.2,
		CBThreshold:             3,
		CBCapacity:              10,
		CBDelay:                 5 * time.Second,
		Telemetry:               telemetry,
	}
}

// BuildPaymentExecutor constructs the failsafe-go Policy Onion for 3rd-party payment gateway calls.
//
// Policy Composition Order (Outer to Inner):
//
//	Fallback( OverallOperationTimeout( RetryPolicy( CircuitBreaker( AttemptTimeout( TargetFunc ) ) ) ) )
//
// 1. Fallback Policy: Catches persistent failures/timeouts and returns a safe degraded result (REVIEW_PENDING).
// 2. Overall Operation Timeout: Strictly limits cumulative execution duration across all attempts to cfg.OverallOperationTimeout (400ms).
// 3. Retry Policy: Retries transient errors (503, network drops) with exponential backoff and randomized jitter.
// 4. Circuit Breaker: Tracks failure ratios and fast-fails immediately if the gateway is dead.
// 5. Per-Attempt Timeout: Enforces a strict socket deadline on each individual try (150ms) to trigger retries promptly.
func BuildPaymentExecutor(cfg PaymentPolicyConfig) (failsafe.Executor[checkout.PaymentResponse], circuitbreaker.CircuitBreaker[checkout.PaymentResponse]) {
	// 1. Overall Operation Timeout (Outer-most upper bound for the entire multi-try operation)
	operationTimeout := timeout.NewBuilder[checkout.PaymentResponse](cfg.OverallOperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[checkout.PaymentResponse]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("payment_gateway_overall", cfg.OverallOperationTimeout.Milliseconds())
			}
		}).
		Build()

	// 2. Fallback Policy (catches unrecoverable errors/timeouts, returning degraded status)
	fallbackPolicy := fallback.NewBuilderWithResult(checkout.PaymentResponse{
		TransactionID: "FALLBACK_REVIEW_PENDING",
		Status:        checkout.PaymentStatusReviewPending,
		ProcessedAt:   time.Now(),
	}).
		HandleErrors(checkout.ErrGatewayUnavailable, checkout.ErrRateLimited, timeout.ErrExceeded, circuitbreaker.ErrOpen).
		OnFallbackExecuted(func(e failsafe.ExecutionDoneEvent[checkout.PaymentResponse]) {
			if cfg.Telemetry != nil {
				reason := "unknown"
				if e.Error != nil {
					reason = e.Error.Error()
				}
				cfg.Telemetry.RecordFallback("payment_gateway", reason)
			}
		}).
		Build()

	// 3. Retry Policy with Exponential Backoff and Jitter
	retryBuilder := retrypolicy.NewBuilder[checkout.PaymentResponse]().
		HandleErrors(checkout.ErrTransientNetwork, checkout.ErrGatewayUnavailable, checkout.ErrRateLimited).
		WithBackoff(cfg.BackoffMin, cfg.BackoffMax).
		WithJitterFactor(cfg.JitterFactor).
		WithMaxRetries(cfg.MaxRetries)

	if cfg.Telemetry != nil {
		retryBuilder.OnRetry(func(e failsafe.ExecutionEvent[checkout.PaymentResponse]) {
			cfg.Telemetry.RecordRetry("payment_gateway", e.Attempts(), e.LastError())
		})
	}
	retryPol := retryBuilder.Build()

	// 4. Circuit Breaker
	cbBuilder := circuitbreaker.NewBuilder[checkout.PaymentResponse]().
		HandleErrors(checkout.ErrGatewayUnavailable, checkout.ErrTransientNetwork).
		WithFailureThresholdRatio(cfg.CBThreshold, cfg.CBCapacity).
		WithDelay(cfg.CBDelay)

	if cfg.Telemetry != nil {
		cbBuilder.OnStateChanged(func(e circuitbreaker.StateChangedEvent) {
			cfg.Telemetry.RecordCircuitBreakerState("payment_gateway", e.NewState.String())
		})
	}
	cb := cbBuilder.Build()

	// 5. Per-Attempt Timeout (Inner-most timeout capping single HTTP request socket)
	attemptTimeout := timeout.NewBuilder[checkout.PaymentResponse](cfg.AttemptTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[checkout.PaymentResponse]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("payment_gateway_attempt", cfg.AttemptTimeout.Milliseconds())
			}
		}).
		Build()

	// Compose Outer to Inner: Fallback -> OverallOperationTimeout -> Retry -> CircuitBreaker -> AttemptTimeout
	executor := failsafe.With(fallbackPolicy, operationTimeout, retryPol, cb, attemptTimeout)
	return executor, cb
}

// ResilientPaymentGateway decorates any checkout.PaymentGateway implementation with the failsafe-go Policy Onion:
// Fallback -> OverallOperationTimeout -> RetryPolicy -> CircuitBreaker -> AttemptTimeout.
type ResilientPaymentGateway struct {
	inner          checkout.PaymentGateway
	executor       failsafe.Executor[checkout.PaymentResponse]
	circuitBreaker circuitbreaker.CircuitBreaker[checkout.PaymentResponse]
}

// NewResilientPaymentGateway wraps an underlying PaymentGateway with production-grade failsafe-go resilience policies.
func NewResilientPaymentGateway(inner checkout.PaymentGateway, cfg PaymentPolicyConfig) *ResilientPaymentGateway {
	executor, cb := BuildPaymentExecutor(cfg)
	return &ResilientPaymentGateway{
		inner:          inner,
		executor:       executor,
		circuitBreaker: cb,
	}
}

// Charge executes the payment request through the multi-policy resilience pipeline.
func (g *ResilientPaymentGateway) Charge(ctx context.Context, req checkout.PaymentRequest) (checkout.PaymentResponse, error) {
	return g.executor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[checkout.PaymentResponse]) (checkout.PaymentResponse, error) {
		resp, err := g.inner.Charge(exec.Context(), req)
		resp.Attempts = exec.Attempts()
		return resp, err
	})
}

// CircuitBreaker returns the underlying circuit breaker for health and state inspection.
func (g *ResilientPaymentGateway) CircuitBreaker() circuitbreaker.CircuitBreaker[checkout.PaymentResponse] {
	return g.circuitBreaker
}
