package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionConnector func(context.Context) (*pgx.Conn, error)

func sessionConnector(pool *pgxpool.Pool, connectors []SessionConnector) SessionConnector {
	if len(connectors) > 0 && connectors[0] != nil {
		return connectors[0]
	}
	return func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
	}
}
