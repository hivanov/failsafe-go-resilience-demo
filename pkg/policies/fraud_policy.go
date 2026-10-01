package policies

import (
	"context"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// FraudPolicyConfig configures the operation timeout and fallback rules for ML fraud evaluation.
type FraudPolicyConfig struct {
	// OperationTimeout sets the maximum time allowed for ML risk scoring.
	OperationTimeout time.Duration

	// Telemetry receives policy lifecycle events.
	Telemetry checkout.TelemetryRecorder
}

// DefaultFraudPolicyConfig returns the default fraud policy configuration (100ms operation timeout).
func DefaultFraudPolicyConfig(telemetry checkout.TelemetryRecorder) FraudPolicyConfig {
	return FraudPolicyConfig{
		OperationTimeout: 100 * time.Millisecond,
		Telemetry:        telemetry,
	}
}

// BuildFraudExecutor constructs an executor wrapping a 100ms Operation Timeout with a heuristic fallback rule.
// If the ML service is slow or times out, it gracefully degrades to a default low-risk evaluation.
func BuildFraudExecutor(cfg FraudPolicyConfig) failsafe.Executor[checkout.RiskScore] {
	tOut := timeout.NewBuilder[checkout.RiskScore](cfg.OperationTimeout).
		OnTimeoutExceeded(func(e failsafe.ExecutionDoneEvent[checkout.RiskScore]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordTimeout("fraud_service_operation", cfg.OperationTimeout.Milliseconds())
			}
		}).
		Build()

	fallbackPol := fallback.NewBuilderWithResult(checkout.RiskScore{
		Score:       20,
		Decision:    checkout.RiskDecisionApproveDegraded,
		Confidence:  0.60,
		IsHeuristic: true,
	}).
		HandleErrors(timeout.ErrExceeded, checkout.ErrTransientNetwork).
		OnFallbackExecuted(func(e failsafe.ExecutionDoneEvent[checkout.RiskScore]) {
			if cfg.Telemetry != nil {
				cfg.Telemetry.RecordFallback("fraud_service", "ml_timeout_degraded_to_heuristic")
			}
		}).
		Build()

	return failsafe.With(fallbackPol, tOut)
}

// ResilientFraudService decorates any checkout.FraudService implementation with an Operation Timeout and heuristic fallback policy.
type ResilientFraudService struct {
	inner    checkout.FraudService
	executor failsafe.Executor[checkout.RiskScore]
}

// NewResilientFraudService wraps a FraudService with timeout and fallback policies.
func NewResilientFraudService(inner checkout.FraudService, cfg FraudPolicyConfig) *ResilientFraudService {
	return &ResilientFraudService{
		inner:    inner,
		executor: BuildFraudExecutor(cfg),
	}
}

// EvaluateRisk executes ML fraud scoring, degrading to heuristic rules if the operation times out.
func (s *ResilientFraudService) EvaluateRisk(ctx context.Context, req checkout.OrderRequest) (checkout.RiskScore, error) {
	return s.executor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[checkout.RiskScore]) (checkout.RiskScore, error) {
		return s.inner.EvaluateRisk(exec.Context(), req)
	})
}
