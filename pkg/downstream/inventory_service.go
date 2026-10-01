package downstream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
)

// ThreadSafeInventoryService implements checkout.InventoryService as a thread-safe,
// in-memory warehouse stock repository simulating database row locking delays and contention.
type ThreadSafeInventoryService struct {
	mu       sync.Mutex
	stock    map[string]int
	delay    time.Duration
	failLock bool
}

// NewInventoryService initializes a new inventory repository with initial SKU stock counts and database query delay.
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

// SetSimulateFailure enables or disables simulated database lock acquisition timeouts.
func (s *ThreadSafeInventoryService) SetSimulateFailure(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLock = fail
}

// LockInventory simulates acquiring a database row lock and decrementing stock.
// Returns checkout.ErrInventoryDepleted if current stock is insufficient.
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

// ReleaseInventory releases a stock reservation, incrementing inventory upon payment rollback.
func (s *ThreadSafeInventoryService) ReleaseInventory(ctx context.Context, itemID string, quantity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stock[itemID] += quantity
	return nil
}

// GetStock returns the current available units for the specified item SKU.
func (s *ThreadSafeInventoryService) GetStock(itemID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stock[itemID]
}
