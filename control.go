package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"stuntgres/internal/engine"
)

// withEngine releases the borrowed connection before performing I/O. The engine
// identity is owned by Driver and outlives the borrowed connection.
func withEngine(ctx context.Context, db *sql.DB, fn func(*engine.Engine) error) error {
	if db == nil {
		return errors.New("stuntgres: nil database")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	var e *engine.Engine
	err = c.Raw(func(raw any) error {
		native, ok := raw.(*conn)
		if !ok {
			return errors.New("stuntgres: controls require a native stuntgres database, not a foreign or wrapped driver")
		}
		if err := native.check(ctx); err != nil {
			return err
		}
		e = native.session.Engine()
		return nil
	})
	closeErr := c.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return fn(e)
}

// Reset atomically clears the named database, retaining its process-local identity.
func Reset(ctx context.Context, db *sql.DB) error {
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Reset(ctx) })
}

// Dump writes a consistent PostgreSQL-compatible plain SQL/INSERT snapshot.
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error {
	if dst == nil {
		return errors.New("stuntgres: nil dump writer")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Dump(ctx, dst) })
}

// Restore atomically replaces the database after the entire bounded CREATE/INSERT
// script, including named SET SESSION AUTHORIZATION owner directives, succeeds.
// Roles named by owner directives must already exist in the target engine.
// Arbitrarily blocking readers cannot be interrupted by context cancellation.
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error {
	if src == nil {
		return errors.New("stuntgres: nil restore reader")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Restore(ctx, src) })
}
