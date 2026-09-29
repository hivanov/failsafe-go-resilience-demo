package checkout

import (
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// PaymentPolicyConfig holds tunable parameters for the payment execution policy
type PaymentPolicyConfig struct {
	OverallTimeout time.Duration
	MaxRetries     int
	BackoffMin     time.Duration
	BackoffMax     time.Duration
	JitterFactor   float64
	CBThreshold    uint
	CBCapacity     uint
	CBDelay        time.Duration
	Telemetry      TelemetryRecorder
}

// DefaultPaymentPolicyConfig returns production-calibrated defaults based on the 800ms SLA
func DefaultPaymentPolicyConfig(telemetry TelemetryRecorder) PaymentPolicyConfig {
	return PaymentPolicyConfig{
		OverallTimeout: 400 * time.Millisecond,
		MaxRetries:     2,
		BackoffMin:     40 * time.Millisecond,
		BackoffMax:     120 * time.Millisecond,
		JitterFactor:   0.2,
		CBThreshold:    3,
		CBCapacity:     10,
		CBDelay:        5 * time.Second,
		Telemetry:      telemetry,
	}
}

// BuildPaymentExecutor builds the failsafe-go Policy Onion for 3rd-party payments.
// Composition order (Outer -> Inner):
// Fallback -> OverallTimeout -> RetryPolicy -> CircuitBreaker
func BuildPaymentExecutor(cfg PaymentPolicyConfig) (failsafe.Executor[PaymentResponse], circuitbreaker.CircuitBreaker[PaymentResponse]) {
	// 1. Overall Time Budget (Outer-most timeout)
	outerTimeout := timeout.NewBuilder[PaymentResponse](cfg.OverallTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[PaymentResponse]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("payment_gateway", cfg.OverallTimeout.Milliseconds())
			}
		}).
		Build()

	// 2. Fallback Policy (catches unrecoverable errors/timeouts, returning degraded status)
	fallbackPolicy := fallback.NewBuilderWithResult(PaymentResponse{
		TransactionID: "FALLBACK_REVIEW_PENDING",
		Status:        "REVIEW_PENDING",
		ProcessedAt:   time.Now(),
	}).
		HandleErrors(ErrGatewayUnavailable, ErrRateLimited, timeout.ErrExceeded, circuitbreaker.ErrOpen).
		OnFallbackExecuted(func(e failsafe.ExecutionDoneEvent[PaymentResponse]) {
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
	retryBuilder := retrypolicy.NewBuilder[PaymentResponse]().
		HandleErrors(ErrTransientNetwork, ErrGatewayUnavailable, ErrRateLimited).
		WithBackoff(cfg.BackoffMin, cfg.BackoffMax).
		WithJitterFactor(cfg.JitterFactor).
		WithMaxRetries(cfg.MaxRetries)

	if cfg.Telemetry != nil {
		retryBuilder.OnRetry(func(e failsafe.ExecutionEvent[PaymentResponse]) {
			cfg.Telemetry.RecordRetry("payment_gateway", e.Attempts(), e.LastError())
		})
	}
	retryPol := retryBuilder.Build()

	// 4. Circuit Breaker
	cbBuilder := circuitbreaker.NewBuilder[PaymentResponse]().
		HandleErrors(ErrGatewayUnavailable, ErrTransientNetwork).
		WithFailureThresholdRatio(cfg.CBThreshold, cfg.CBCapacity).
		WithDelay(cfg.CBDelay)

	if cfg.Telemetry != nil {
		cbBuilder.OnStateChanged(func(e circuitbreaker.StateChangedEvent) {
			cfg.Telemetry.RecordCircuitBreakerState("payment_gateway", e.NewState.String())
		})
	}
	cb := cbBuilder.Build()

	// Compose Outer to Inner
	executor := failsafe.With(fallbackPolicy, outerTimeout, retryPol, cb)
	return executor, cb
}

// FraudPolicyConfig configures the semi-critical ML fraud evaluation policy
type FraudPolicyConfig struct {
	Timeout   time.Duration
	Telemetry TelemetryRecorder
}

// BuildFraudExecutor constructs the bounded fallback executor for fraud checks
func BuildFraudExecutor(cfg FraudPolicyConfig) failsafe.Executor[RiskScore] {
	tOut := timeout.NewBuilder[RiskScore](cfg.Timeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[RiskScore]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("fraud_service", cfg.Timeout.Milliseconds())
			}
		}).
		Build()

	fallbackPol := fallback.NewBuilderWithResult(RiskScore{
		Score:       20,
		Decision:    "APPROVE_DEGRADED",
		Confidence:  0.60,
		IsHeuristic: true,
	}).
		HandleErrors(timeout.ErrExceeded, ErrTransientNetwork).
		OnFallbackExecuted(func(e failsafe.ExecutionDoneEvent[RiskScore]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordFallback("fraud_service", "ml_timeout_degraded_to_heuristic")
			}
		}).
		Build()

	return failsafe.With(fallbackPol, tOut)
}
