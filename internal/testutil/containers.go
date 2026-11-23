// Package testutil starts throwaway infrastructure for integration tests.
package testutil

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"fincore/db"
)

const (
	PostgresImage  = "postgres:17-alpine"
	DragonflyImage = "docker.dragonflydb.io/dragonflydb/dragonfly:v1.27.1"
)

// StartPostgres starts a migrated Postgres and returns a pool and a cleanup.
func StartPostgres(ctx context.Context) (*pgxpool.Pool, func(), error) {
	ctr, err := postgres.Run(ctx, PostgresImage,
		postgres.WithDatabase("fincore_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres: %w", err)
	}
	cleanup := func() { _ = ctr.Terminate(context.Background()) }

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := db.Migrate(ctx, url, "up"); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return pool, func() { pool.Close(); cleanup() }, nil
}

// StartDragonfly starts Dragonfly and returns a connected client and a cleanup.
func StartDragonfly(ctx context.Context) (*redis.Client, func(), error) {
	ctr, err := testcontainers.Run(ctx, DragonflyImage,
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp")),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start dragonfly: %w", err)
	}
	cleanup := func() { _ = ctr.Terminate(context.Background()) }
	endpoint, err := ctr.PortEndpoint(ctx, "6379/tcp", "")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	rdb := redis.NewClient(&redis.Options{Addr: endpoint})
	if err := rdb.Ping(ctx).Err(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("ping dragonfly: %w", err)
	}
	return rdb, func() { _ = rdb.Close(); cleanup() }, nil
}
