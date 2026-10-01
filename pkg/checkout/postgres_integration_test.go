package checkout_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	"github.com/failsafe-go-demo/checkout/pkg/downstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestPostgres_Integration verifies the entire checkout orchestrator against a REAL PostgreSQL Testcontainer.
// Proves atomic row-locking, inventory deduction, and concrete transactional rollback upon payment failure.
func TestPostgres_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainer integration test in short mode")
	}

	// Auto-configure Docker / Podman environment variables for Testcontainers
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	if os.Getenv("DOCKER_HOST") == "" {
		podmanSock := os.ExpandEnv("$HOME/.local/share/containers/podman/machine/podman.sock")
		if _, err := os.Stat(podmanSock); err == nil {
			_ = os.Setenv("DOCKER_HOST", "unix://"+podmanSock)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Spin up real PostgreSQL 16 container via Testcontainers
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("checkout_test_db"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres testcontainer: %v", err)
	}
	defer func() {
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	pgInv := downstream.NewPostgresInventoryService(db)
	if err := pgInv.InitSchema(ctx); err != nil {
		t.Fatalf("failed to init postgres schema: %v", err)
	}

	// Seed SKU: sku-real-macbook with 10 units
	const itemSKU = "sku-real-macbook"
	if err := pgInv.SeedStock(ctx, itemSKU, 10); err != nil {
		t.Fatalf("failed to seed stock: %v", err)
	}

	telemetry := checkout.NewInMemoryTelemetry()
	payGateway := downstream.NewSimulatedPaymentGateway(15 * time.Millisecond)
	fraudSvc := downstream.NewFraudService(10 * time.Millisecond)
	loyaltySvc := downstream.NewLoyaltyService()

	payPolicy := checkout.DefaultPaymentPolicyConfig(telemetry)
	invPolicy := checkout.DefaultInventoryPolicyConfig(telemetry)
	fraudPolicy := checkout.DefaultFraudPolicyConfig(telemetry)
	loyaltyPolicy := checkout.LoyaltyPolicyConfig{OperationTimeout: 200 * time.Millisecond, Telemetry: telemetry}

	orch := checkout.NewOrchestrator(payGateway, pgInv, fraudSvc, loyaltySvc, telemetry, payPolicy, invPolicy, fraudPolicy, loyaltyPolicy)

	// -------------------------------------------------------------------------
	// 1. Happy Path: Purchase 2 units -> Stock must be exactly 8 in Postgres
	// -------------------------------------------------------------------------
	t.Run("HappyPath_LivePostgres", func(t *testing.T) {
		res, err := orch.ProcessOrder(ctx, checkout.OrderRequest{
			OrderID:     "ORD-PG-001",
			CustomerID:  "CUST-PG-1",
			ItemID:      itemSKU,
			Quantity:    2,
			Amount:      2400.0,
			Currency:    "EUR",
			Idempotency: "idem-pg-001",
		})
		if err != nil {
			t.Fatalf("expected order success, got error: %v", err)
		}
		if res.Status != "SUCCESS" {
			t.Errorf("expected status SUCCESS, got %s", res.Status)
		}

		// Verify concrete SQL data in Postgres
		stock, err := pgInv.GetStock(ctx, itemSKU)
		if err != nil {
			t.Fatalf("failed to query postgres stock: %v", err)
		}
		if stock != 8 {
			t.Errorf("expected remaining stock in postgres = 8, got %d", stock)
		}
	})

	// -------------------------------------------------------------------------
	// 2. Payment Failure: Purchase 3 units but Payment Fails -> Must rollback stock to 8
	// -------------------------------------------------------------------------
	t.Run("PaymentFailure_RollbackInPostgres", func(t *testing.T) {
		payGateway.Reset()
		payGateway.QueueFailures(checkout.ErrInvalidPayment) // Non-retryable 400 rejection

		res, err := orch.ProcessOrder(ctx, checkout.OrderRequest{
			OrderID:     "ORD-PG-002",
			CustomerID:  "CUST-PG-2",
			ItemID:      itemSKU,
			Quantity:    3,
			Amount:      3600.0,
			Currency:    "EUR",
			Idempotency: "idem-pg-002",
		})
		if err == nil {
			t.Fatalf("expected payment error, got nil result: %+v", res)
		}
		if res.Status != "FAILED" {
			t.Errorf("expected status FAILED, got %s", res.Status)
		}

		// Verify rollback: stock must remain 8
		stock, err := pgInv.GetStock(ctx, itemSKU)
		if err != nil {
			t.Fatalf("failed to query postgres stock: %v", err)
		}
		if stock != 8 {
			t.Errorf("expected rolled-back stock in postgres = 8, got %d", stock)
		}
	})

	// -------------------------------------------------------------------------
	// 3. High-Concurrency Burst against Live Postgres: 8 concurrent buyers for 1 unit each
	// -------------------------------------------------------------------------
	t.Run("ConcurrentPurchases_ZeroDoubleSpending", func(t *testing.T) {
		payGateway.Reset()

		const concurrentOrders = 8
		var wg sync.WaitGroup
		var successCount int64

		wg.Add(concurrentOrders)
		for i := 0; i < concurrentOrders; i++ {
			go func(buyerID int) {
				defer wg.Done()
				res, err := orch.ProcessOrder(context.Background(), checkout.OrderRequest{
					OrderID:     fmt.Sprintf("ORD-PG-CONC-%03d", buyerID),
					CustomerID:  fmt.Sprintf("CUST-BUYER-%d", buyerID),
					ItemID:      itemSKU,
					Quantity:    1,
					Amount:      1200.0,
					Currency:    "EUR",
					Idempotency: fmt.Sprintf("idem-buyer-%d", buyerID),
				})
				if err == nil && res.Status == "SUCCESS" {
					atomic.AddInt64(&successCount, 1)
				}
			}(i)
		}

		wg.Wait()

		if atomic.LoadInt64(&successCount) != concurrentOrders {
			t.Errorf("expected %d successful orders, got %d", concurrentOrders, successCount)
		}

		// Final stock in Postgres must be exactly 0 (8 - 8 = 0)
		stock, err := pgInv.GetStock(ctx, itemSKU)
		if err != nil {
			t.Fatalf("failed to query postgres stock: %v", err)
		}
		if stock != 0 {
			t.Errorf("expected final stock in postgres = 0, got %d", stock)
		}

		// Attempting 1 more purchase must fail with ErrInventoryDepleted
		_, err = orch.ProcessOrder(ctx, checkout.OrderRequest{
			OrderID:    "ORD-PG-OVERDRAFT",
			CustomerID: "CUST-OVERDRAFT",
			ItemID:     itemSKU,
			Quantity:   1,
			Amount:     1200.0,
		})
		if err != checkout.ErrInventoryDepleted {
			t.Errorf("expected ErrInventoryDepleted on depleted stock, got %v", err)
		}
	})
}
