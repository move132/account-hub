package sqlite

import (
	"context"
	"database/sql"
)

// DBTX lets repositories join an application-owned transaction without starting another one.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
