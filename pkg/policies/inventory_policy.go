package policies

import (
	"context"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// InventoryPolicyConfig configures the operation timeout policy for Inventory row locking.
type InventoryPolicyConfig struct {
	// OperationTimeout sets the maximum time allowed to acquire a database lock.
	OperationTimeout time.Duration

	// Telemetry receives timeout events.
	Telemetry checkout.TelemetryRecorder
}

// DefaultInventoryPolicyConfig returns the default inventory policy configuration (150ms operation timeout).
func DefaultInventoryPolicyConfig(telemetry checkout.TelemetryRecorder) InventoryPolicyConfig {
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

// ResilientInventoryService decorates any checkout.InventoryService implementation with an explicit Operation Timeout policy.
type ResilientInventoryService struct {
	inner    checkout.InventoryService
	executor failsafe.Executor[any]
}

// NewResilientInventoryService wraps an InventoryService with an Operation Timeout.
func NewResilientInventoryService(inner checkout.InventoryService, cfg InventoryPolicyConfig) *ResilientInventoryService {
	return &ResilientInventoryService{
		inner:    inner,
		executor: BuildInventoryExecutor(cfg),
	}
}

// LockInventory executes lock acquisition bounded by the operation timeout policy.
func (s *ResilientInventoryService) LockInventory(ctx context.Context, itemID string, quantity int) error {
	return s.executor.WithContext(ctx).RunWithExecution(func(exec failsafe.Execution[any]) error {
		return s.inner.LockInventory(exec.Context(), itemID, quantity)
	})
}

// ReleaseInventory passes through directly to the underlying service implementation.
func (s *ResilientInventoryService) ReleaseInventory(ctx context.Context, itemID string, quantity int) error {
	return s.inner.ReleaseInventory(ctx, itemID, quantity)
}
