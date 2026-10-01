package downstream

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/failsafe-go-demo/checkout/pkg/checkout"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresInventoryService implements checkout.InventoryService backed by a live PostgreSQL database
// using transactional row-level locking (SELECT ... FOR UPDATE) and atomic rollbacks.
type PostgresInventoryService struct {
	db *sql.DB
}

// NewPostgresInventoryService initializes a new PostgreSQL inventory service with an active database connection pool.
func NewPostgresInventoryService(db *sql.DB) *PostgresInventoryService {
	return &PostgresInventoryService{db: db}
}

// InitSchema creates the inventory table schema and indexes if they do not exist.
func (s *PostgresInventoryService) InitSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS inventory (
		item_id VARCHAR(64) PRIMARY KEY,
		stock INT NOT NULL CHECK (stock >= 0),
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := s.db.ExecContext(ctx, query)
	return err
}

// SeedStock sets the inventory count for a given item SKU.
func (s *PostgresInventoryService) SeedStock(ctx context.Context, itemID string, stock int) error {
	query := `
	INSERT INTO inventory (item_id, stock, updated_at)
	VALUES ($1, $2, CURRENT_TIMESTAMP)
	ON CONFLICT (item_id) DO UPDATE SET stock = EXCLUDED.stock, updated_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.ExecContext(ctx, query, itemID, stock)
	return err
}

// LockInventory acquires a row lock and decrements available inventory stock inside a PostgreSQL transaction.
func (s *PostgresInventoryService) LockInventory(ctx context.Context, itemID string, quantity int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var currentStock int
	query := `SELECT stock FROM inventory WHERE item_id = $1 FOR UPDATE`
	row := tx.QueryRowContext(ctx, query, itemID)
	if err := row.Scan(&currentStock); err != nil {
		if err == sql.ErrNoRows {
			return checkout.ErrInventoryDepleted
		}
		return fmt.Errorf("failed to query inventory row lock: %w", err)
	}

	if currentStock < quantity {
		return checkout.ErrInventoryDepleted
	}

	updateQuery := `UPDATE inventory SET stock = stock - $1, updated_at = CURRENT_TIMESTAMP WHERE item_id = $2`
	if _, err := tx.ExecContext(ctx, updateQuery, quantity, itemID); err != nil {
		return fmt.Errorf("failed to decrement stock: %w", err)
	}

	return tx.Commit()
}

// ReleaseInventory increments stock units upon transaction rollback.
func (s *PostgresInventoryService) ReleaseInventory(ctx context.Context, itemID string, quantity int) error {
	query := `UPDATE inventory SET stock = stock + $1, updated_at = CURRENT_TIMESTAMP WHERE item_id = $2`
	_, err := s.db.ExecContext(ctx, query, quantity, itemID)
	return err
}

// GetStock returns the current live stock count directly from the PostgreSQL table.
func (s *PostgresInventoryService) GetStock(ctx context.Context, itemID string) (int, error) {
	var stock int
	query := `SELECT stock FROM inventory WHERE item_id = $1`
	err := s.db.QueryRowContext(ctx, query, itemID).Scan(&stock)
	return stock, err
}
