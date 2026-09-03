package database

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	url := os.Getenv("DEVPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVPILOT_TEST_DATABASE_URL is not set")
	}
	connection, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background()) //nolint:errcheck
	if err := Migrate(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), connection); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	var count int
	if err := connection.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration count = %d, want 2", count)
	}
}
