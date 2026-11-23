// Package db embeds the SQL migrations so binaries and tests apply exactly
// the same schema.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

//go:embed migrations/*.sql
var Migrations embed.FS

// Migrate runs goose with the given command ("up", "down", "status", ...).
func Migrate(ctx context.Context, databaseURL, command string, args ...string) error {
	conn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	goose.SetBaseFS(Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.RunContext(ctx, command, conn, "migrations", args...)
}
