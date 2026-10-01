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
	OverallOperationTimeout time.Duration // Hard upper bound for cumulative operation
	AttemptTimeout          time.Duration // Per-attempt socket timeout
	MaxRetries              int
	BackoffMin              time.Duration
	BackoffMax              time.Duration
	JitterFactor            float64
	CBThreshold             uint
	CBCapacity              uint
	CBDelay                 time.Duration
	Telemetry               TelemetryRecorder
}

// DefaultPaymentPolicyConfig returns production-calibrated defaults based on the 800ms SLA
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

// BuildPaymentExecutor builds the failsafe-go Policy Onion for 3rd-party payments.
// Composition order (Outer -> Inner):
// Fallback -> OverallOperationTimeout (400ms) -> RetryPolicy -> CircuitBreaker -> AttemptTimeout (150ms) -> Target
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

// InventoryPolicyConfig configures the operation timeout policy for Inventory row locking
type InventoryPolicyConfig struct {
	OperationTimeout time.Duration // 150ms budget ceiling
	Telemetry        TelemetryRecorder
}

// DefaultInventoryPolicyConfig returns default inventory policy config
func DefaultInventoryPolicyConfig(telemetry TelemetryRecorder) InventoryPolicyConfig {
	return InventoryPolicyConfig{
		OperationTimeout: 150 * time.Millisecond,
		Telemetry:        telemetry,
	}
}

// BuildInventoryExecutor constructs the Operation Timeout policy executor for Inventory
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

// FraudPolicyConfig configures the semi-critical ML fraud evaluation policy
type FraudPolicyConfig struct {
	OperationTimeout time.Duration // 100ms budget upper bound
	Telemetry        TelemetryRecorder
}

// DefaultFraudPolicyConfig returns default fraud policy config
func DefaultFraudPolicyConfig(telemetry TelemetryRecorder) FraudPolicyConfig {
	return FraudPolicyConfig{
		OperationTimeout: 100 * time.Millisecond,
		Telemetry:        telemetry,
	}
}

// BuildFraudExecutor constructs the bounded fallback executor for fraud checks
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

// LoyaltyPolicyConfig configures the async loyalty accrual operation timeout
type LoyaltyPolicyConfig struct {
	OperationTimeout time.Duration // 200ms background budget
	Telemetry        TelemetryRecorder
}

// BuildLoyaltyExecutor constructs the Operation Timeout executor for async loyalty
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
