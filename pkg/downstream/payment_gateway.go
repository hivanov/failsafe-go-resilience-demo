// Package downstream provides thread-safe, realistic simulator implementations of external
// payment gateways, database inventory repositories, ML risk engines, and loyalty ledgers
// for local testing, demonstration, and fault injection without synthetic mocks.
package downstream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// SimulatedPaymentGateway implements checkout.PaymentGateway with realistic latency simulation,
// programmable error queues, custom handler hooks, and atomic call counting.
type SimulatedPaymentGateway struct {
	mu            sync.Mutex
	callCount     int64
	baseLatency   time.Duration
	failureQueue  []error
	customHandler func(ctx context.Context, req checkout.PaymentRequest, attempt int) (checkout.PaymentResponse, error)
}

// NewSimulatedPaymentGateway creates a new payment gateway simulator with a base I/O latency.
func NewSimulatedPaymentGateway(baseLatency time.Duration) *SimulatedPaymentGateway {
	return &SimulatedPaymentGateway{
		baseLatency:  baseLatency,
		failureQueue: make([]error, 0),
	}
}

// QueueFailures appends error instances to the simulator's sequential failure queue.
// Invocations of Charge will dequeue and return these errors in FIFO order.
func (g *SimulatedPaymentGateway) QueueFailures(errs ...error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failureQueue = append(g.failureQueue, errs...)
}

// SetCustomHandler registers a dynamic execution hook to override default latency and error handling.
func (g *SimulatedPaymentGateway) SetCustomHandler(h func(ctx context.Context, req checkout.PaymentRequest, attempt int) (checkout.PaymentResponse, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.customHandler = h
}

// Charge processes a credit card charge, simulating network round-trip latency while respecting context cancellation.
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
		Status:        checkout.PaymentStatusSettled,
		ProcessedAt:   time.Now(),
	}, nil
}

// TotalCalls returns the cumulative number of Charge invocations attempted.
func (g *SimulatedPaymentGateway) TotalCalls() int64 {
	return atomic.LoadInt64(&g.callCount)
}

// Reset clears call counters, failure queues, and custom hooks, returning the gateway to a healthy baseline.
func (g *SimulatedPaymentGateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	atomic.StoreInt64(&g.callCount, 0)
	g.failureQueue = make([]error, 0)
	g.customHandler = nil
}
