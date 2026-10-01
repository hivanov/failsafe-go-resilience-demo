package checkout

import (
	"time"

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
	Telemetry TelemetryRecorder
}

// DefaultPaymentPolicyConfig returns production-calibrated defaults derived from the 800ms business SLA.
// Allocates a 400ms overall operation timeout, 150ms per-attempt timeout, up to 2 retries with jitter,
// and a 3/10 failure ratio circuit breaker.
func DefaultPaymentPolicyConfig(telemetry TelemetryRecorder) PaymentPolicyConfig {
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
func BuildPaymentExecutor(cfg PaymentPolicyConfig) (failsafe.Executor[PaymentResponse], circuitbreaker.CircuitBreaker[PaymentResponse]) {
	// 1. Overall Operation Timeout (Outer-most upper bound for the entire multi-try operation)
	operationTimeout := timeout.NewBuilder[PaymentResponse](cfg.OverallOperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[PaymentResponse]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("payment_gateway_overall", cfg.OverallOperationTimeout.Milliseconds())
			}
		}).
		Build()

	// 2. Fallback Policy (catches unrecoverable errors/timeouts, returning degraded status)
	fallbackPolicy := fallback.NewBuilderWithResult(PaymentResponse{
		TransactionID: "FALLBACK_REVIEW_PENDING",
		Status:        PaymentStatusReviewPending,
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

	// 5. Per-Attempt Timeout (Inner-most timeout capping single HTTP request socket)
	attemptTimeout := timeout.NewBuilder[PaymentResponse](cfg.AttemptTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[PaymentResponse]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("payment_gateway_attempt", cfg.AttemptTimeout.Milliseconds())
			}
		}).
		Build()

	// Compose Outer to Inner: Fallback -> OverallOperationTimeout -> Retry -> CircuitBreaker -> AttemptTimeout
	executor := failsafe.With(fallbackPolicy, operationTimeout, retryPol, cb, attemptTimeout)
	return executor, cb
}

// InventoryPolicyConfig configures the operation timeout policy for Inventory row locking.
type InventoryPolicyConfig struct {
	// OperationTimeout sets the maximum time allowed to acquire a database lock.
	OperationTimeout time.Duration

	// Telemetry receives timeout events.
	Telemetry TelemetryRecorder
}

// DefaultInventoryPolicyConfig returns the default inventory policy configuration (150ms operation timeout).
func DefaultInventoryPolicyConfig(telemetry TelemetryRecorder) InventoryPolicyConfig {
	return InventoryPolicyConfig{
		OperationTimeout: 150 * time.Millisecond,
		Telemetry:        telemetry,
	}
}

// BuildInventoryExecutor constructs a failsafe.Executor enforcing an explicit Operation Timeout on inventory locking.
func BuildInventoryExecutor(cfg InventoryPolicyConfig) failsafe.Executor[any] {
	opTimeout := timeout.NewBuilder[any](cfg.OperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[any]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("inventory_lock_operation", cfg.OperationTimeout.Milliseconds())
			}
		}).
		Build()

	return failsafe.With(opTimeout)
}

// FraudPolicyConfig configures the operation timeout and fallback rules for ML fraud evaluation.
type FraudPolicyConfig struct {
	// OperationTimeout sets the maximum time allowed for ML risk scoring.
	OperationTimeout time.Duration

	// Telemetry receives policy lifecycle events.
	Telemetry TelemetryRecorder
}

// DefaultFraudPolicyConfig returns the default fraud policy configuration (100ms operation timeout).
func DefaultFraudPolicyConfig(telemetry TelemetryRecorder) FraudPolicyConfig {
	return FraudPolicyConfig{
		OperationTimeout: 100 * time.Millisecond,
		Telemetry:        telemetry,
	}
}

// BuildFraudExecutor constructs an executor wrapping a 100ms Operation Timeout with a heuristic fallback rule.
// If the ML service is slow or times out, it gracefully degrades to a default low-risk evaluation.
func BuildFraudExecutor(cfg FraudPolicyConfig) failsafe.Executor[RiskScore] {
	tOut := timeout.NewBuilder[RiskScore](cfg.OperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[RiskScore]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("fraud_service_operation", cfg.OperationTimeout.Milliseconds())
			}
		}).
		Build()

	fallbackPol := fallback.NewBuilderWithResult(RiskScore{
		Score:       20,
		Decision:    RiskDecisionApproveDegraded,
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

// LoyaltyPolicyConfig configures the Operation Timeout for asynchronous background loyalty accrual.
type LoyaltyPolicyConfig struct {
	// OperationTimeout sets the maximum budget for async point calculations.
	OperationTimeout time.Duration

	// Telemetry receives timeout events.
	Telemetry TelemetryRecorder
}

// BuildLoyaltyExecutor constructs a failsafe.Executor enforcing an explicit Operation Timeout on async loyalty operations.
func BuildLoyaltyExecutor(cfg LoyaltyPolicyConfig) failsafe.Executor[any] {
	tOut := timeout.NewBuilder[any](cfg.OperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[any]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("loyalty_operation", cfg.OperationTimeout.Milliseconds())
			}
		}).
		Build()

	return failsafe.With(tOut)
}
