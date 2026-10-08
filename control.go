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

// Reset atomically clears persistent objects, retaining database identity,
// roles, session settings and live session-private temporary sequences.
func Reset(ctx context.Context, db *sql.DB) error {
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Reset(ctx) })
}

// Dump writes a consistent PostgreSQL-compatible persistent SQL snapshot,
// including sequence options, ownership, comments and called generator state.
// Named owners must already exist in the destination. Each relation owner group
// explicitly selects its session authorization, including the bootstrap owner;
// all tables precede sequence OWNED BY associations that depend on them.
// Session-private temporary objects and pending session caches are not serialized.
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error {
	if dst == nil {
		return errors.New("stuntgres: nil dump writer")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Dump(ctx, dst) })
}

// Restore atomically replaces persistent objects after supported CREATE/ALTER/DROP
// SCHEMA, CREATE TABLE/SEQUENCE/VIEW, parameter-free scalar INSERT (including integer
// arithmetic), database-local COMMENT, sequence OWNED BY associations, generated
// SELECT setval(name,value,is_called), and named owner directives succeed.
// Parameters are recursively rejected; other statements remain excluded.
// ROLE comments are shared authority metadata: Reset and Restore preserve them,
// and Dump excludes them. Query INSERT sources are deliberately not Restore
// syntax; Dump emits canonical literal VALUES.
// Caller-owned temporary sequences remain live and are excluded by Dump.
// Its initialized private catalog preserves legacy streams and reconstructs
// bootstrap namespace lifecycle changes without synthesizing ACL grants.
// Named owners must already exist. Private Restore-stage CREATE TABLE destination
// authority preserves independent schema/table owners without adding ACL grants.
// Private stage INSERT and database-local COMMENT use validated captured bootstrap
// authority without changing named owner directives, session identity or ordinary
// SQL authorization. Neither command stamps ownership or synthesizes ACL grants.
// Arbitrarily blocking readers cannot be interrupted by context cancellation.
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error {
	if src == nil {
		return errors.New("stuntgres: nil restore reader")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Restore(ctx, src) })
}
