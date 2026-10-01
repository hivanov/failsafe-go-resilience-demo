package downstream

import (
	"context"
	"sync"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// SimulatedFraudService implements checkout.FraudService, simulating an internal
// machine learning risk assessment microservice with configurable latency and decision overrides.
type SimulatedFraudService struct {
	mu             sync.Mutex
	latency        time.Duration
	forcedScore    int
	forcedDecision checkout.RiskDecision
}

// NewFraudService creates a new ML fraud service simulator with a baseline model evaluation latency.
func NewFraudService(latency time.Duration) *SimulatedFraudService {
	return &SimulatedFraudService{
		latency:        latency,
		forcedScore:    10,
		forcedDecision: checkout.RiskDecisionApprove,
	}
}

// SetOverride overrides the default evaluation score and risk decision outcome.
func (f *SimulatedFraudService) SetOverride(score int, decision checkout.RiskDecision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forcedScore = score
	f.forcedDecision = decision
}

// SetLatency updates the simulated model evaluation delay (e.g. to trigger timeouts).
func (f *SimulatedFraudService) SetLatency(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency = d
}

// EvaluateRisk simulates executing the ML fraud scoring pipeline.
func (f *SimulatedFraudService) EvaluateRisk(ctx context.Context, req checkout.OrderRequest) (checkout.RiskScore, error) {
	f.mu.Lock()
	lat := f.latency
	score := f.forcedScore
	decision := f.forcedDecision
	f.mu.Unlock()

	select {
	case <-time.After(lat):
	case <-ctx.Done():
		return checkout.RiskScore{}, ctx.Err()
	}

	return checkout.RiskScore{
		Score:       score,
		Decision:    decision,
		Confidence:  0.95,
		IsHeuristic: false,
	}, nil
}

// SimulatedLoyaltyService implements checkout.LoyaltyService, tracking accumulated reward credits.
type SimulatedLoyaltyService struct {
	mu           sync.Mutex
	AccruedTotal float64
	CallCount    int
}

// NewLoyaltyService creates a new loyalty ledger simulator.
func NewLoyaltyService() *SimulatedLoyaltyService {
	return &SimulatedLoyaltyService{}
}

// AccruePoints simulates updating customer loyalty point ledger in the background.
func (l *SimulatedLoyaltyService) AccruePoints(ctx context.Context, customerID string, amount float64) error {
	select {
	case <-time.After(15 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.AccruedTotal += amount * 0.05
	l.CallCount++
	return nil
}
