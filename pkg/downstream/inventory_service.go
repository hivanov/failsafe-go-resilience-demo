package downstream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// ThreadSafeInventoryService simulates database inventory table row locking
type ThreadSafeInventoryService struct {
	mu       sync.Mutex
	stock    map[string]int
	delay    time.Duration
	failLock bool
}

// NewInventoryService initializes inventory stock
func NewInventoryService(initialStock map[string]int, dbLatency time.Duration) *ThreadSafeInventoryService {
	copied := make(map[string]int)
	for k, v := range initialStock {
		copied[k] = v
	}
	return &ThreadSafeInventoryService{
		stock: copied,
		delay: dbLatency,
	}
}

func (s *ThreadSafeInventoryService) SetSimulateFailure(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLock = fail
}

func (s *ThreadSafeInventoryService) LockInventory(ctx context.Context, itemID string, quantity int) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failLock {
		return fmt.Errorf("database lock acquisition timeout for item %s", itemID)
	}

	current, exists := s.stock[itemID]
	if !exists || current < quantity {
		return checkout.ErrInventoryDepleted
	}

	s.stock[itemID] -= quantity
	return nil
}

func (s *ThreadSafeInventoryService) ReleaseInventory(ctx context.Context, itemID string, quantity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stock[itemID] += quantity
	return nil
}

func (s *ThreadSafeInventoryService) GetStock(itemID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stock[itemID]
}
