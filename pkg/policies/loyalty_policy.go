package policies

import (
	"context"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// LoyaltyPolicyConfig configures the Operation Timeout for asynchronous background loyalty accrual.
type LoyaltyPolicyConfig struct {
	// OperationTimeout sets the maximum budget for async point calculations.
	OperationTimeout time.Duration

	// Telemetry receives timeout events.
	Telemetry checkout.TelemetryRecorder
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

// ResilientLoyaltyService decorates any checkout.LoyaltyService implementation with an async Operation Timeout policy.
type ResilientLoyaltyService struct {
	inner    checkout.LoyaltyService
	executor failsafe.Executor[any]
}

// NewResilientLoyaltyService wraps a LoyaltyService with an Operation Timeout.
func NewResilientLoyaltyService(inner checkout.LoyaltyService, cfg LoyaltyPolicyConfig) *ResilientLoyaltyService {
	return &ResilientLoyaltyService{
		inner:    inner,
		executor: BuildLoyaltyExecutor(cfg),
	}
}

// AccruePoints executes reward accrual bounded by the operation timeout.
func (s *ResilientLoyaltyService) AccruePoints(ctx context.Context, customerID string, amount float64) error {
	return s.executor.WithContext(ctx).RunWithExecution(func(exec failsafe.Execution[any]) error {
		return s.inner.AccruePoints(exec.Context(), customerID, amount)
	})
}
