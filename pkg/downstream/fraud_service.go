package downstream

import (
	"context"
	"sync"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// SimulatedFraudService simulates an internal ML risk-scoring microservice
type SimulatedFraudService struct {
	mu            sync.Mutex
	latency       time.Duration
	forcedScore   int
	forcedDecision string
}

func NewFraudService(latency time.Duration) *SimulatedFraudService {
	return &SimulatedFraudService{
		latency:        latency,
		forcedScore:    10,
		forcedDecision: "APPROVE",
	}
}

func (f *SimulatedFraudService) SetOverride(score int, decision string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forcedScore = score
	f.forcedDecision = decision
}

func (f *SimulatedFraudService) SetLatency(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency = d
}

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

// SimulatedLoyaltyService records async rewards
type SimulatedLoyaltyService struct {
	mu           sync.Mutex
	AccruedTotal float64
	CallCount    int
}

func NewLoyaltyService() *SimulatedLoyaltyService {
	return &SimulatedLoyaltyService{}
}

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
