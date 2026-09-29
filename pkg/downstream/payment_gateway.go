package downstream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// SimulatedPaymentGateway implements checkout.PaymentGateway with realistic latency and programmable failure injection.
type SimulatedPaymentGateway struct {
	mu            sync.Mutex
	callCount     int64
	baseLatency   time.Duration
	failureQueue  []error
	customHandler func(ctx context.Context, req checkout.PaymentRequest, attempt int) (checkout.PaymentResponse, error)
}

// NewSimulatedPaymentGateway creates a new payment gateway simulator
func NewSimulatedPaymentGateway(baseLatency time.Duration) *SimulatedPaymentGateway {
	return &SimulatedPaymentGateway{
		baseLatency:  baseLatency,
		failureQueue: make([]error, 0),
	}
}

// QueueFailures queues errors for sequential attempts
func (g *SimulatedPaymentGateway) QueueFailures(errs ...error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failureQueue = append(g.failureQueue, errs...)
}

// SetCustomHandler allows custom simulation hooks
func (g *SimulatedPaymentGateway) SetCustomHandler(h func(ctx context.Context, req checkout.PaymentRequest, attempt int) (checkout.PaymentResponse, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.customHandler = h
}

// Charge executes a payment request with context-aware sleep and failure handling
func (g *SimulatedPaymentGateway) Charge(ctx context.Context, req checkout.PaymentRequest) (checkout.PaymentResponse, error) {
	attempt := atomic.AddInt64(&g.callCount, 1)

	g.mu.Lock()
	handler := g.customHandler
	var queuedErr error
	if len(g.failureQueue) > 0 {
		queuedErr = g.failureQueue[0]
		g.failureQueue = g.failureQueue[1:]
	}
	g.mu.Unlock()

	if handler != nil {
		return handler(ctx, req, int(attempt))
	}

	// Simulate I/O latency respecting context cancellation
	select {
	case <-time.After(g.baseLatency):
	case <-ctx.Done():
		return checkout.PaymentResponse{}, ctx.Err()
	}

	if queuedErr != nil {
		return checkout.PaymentResponse{}, queuedErr
	}

	return checkout.PaymentResponse{
		TransactionID: fmt.Sprintf("tx_pay_%s_%d", req.OrderID, attempt),
		Status:        "SETTLED",
		ProcessedAt:   time.Now(),
	}, nil
}

// TotalCalls returns the total count of Charge invocations
func (g *SimulatedPaymentGateway) TotalCalls() int64 {
	return atomic.LoadInt64(&g.callCount)
}

// Reset resets call counters and failure queues
func (g *SimulatedPaymentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	atomic.StoreInt64(&g.callCount, 0)
	g.failureQueue = make([]error, 0)
	g.customHandler = nil
}
