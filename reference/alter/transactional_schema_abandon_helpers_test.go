package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func transactionalSchemaAbandonment(t *testing.T) {
	t.Run("physical_abandonment", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "tx_abandon_private")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				fixture.LockExec(t, p.Ctx, b.DB, "CREATE TABLE tx_abandon_committed(v INT DEFAULT 7)", 0)
				fixture.LockExec(t, p.Ctx, b.DB, "INSERT INTO tx_abandon_committed DEFAULT VALUES", 1)
				pool := b.DB
				if !b.PG {
					pool = sql.OpenDB(p.NativeConnector)
					defer pool.Close()
					pool.SetMaxOpenConns(1)
					pool.SetMaxIdleConns(1)
				}
				owner, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				pid := transactionalSchemaPID(t, p, b, peer)
				var oid int64
				if err := peer.QueryRowContext(p.Ctx, "SELECT 'tx_abandon_committed'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
				if b.PG {
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
				}
				fixture.LockExec(t, p.Ctx, owner, "CREATE SCHEMA tx_abandon_private CREATE TABLE owned(id SERIAL PRIMARY KEY)", 0)
				fixture.LockExec(t, p.Ctx, owner, "INSERT INTO tx_abandon_private.owned DEFAULT VALUES", 1)
				fixture.LockExec(t, p.Ctx, owner, "ALTER TABLE tx_abandon_committed ALTER COLUMN v SET DEFAULT 12", 0)
				fixture.LockExec(t, p.Ctx, owner, "INSERT INTO tx_abandon_committed DEFAULT VALUES", 1)
				done := make(chan struct{})
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					rows, err := peer.QueryContext(ctx, "SELECT v FROM tx_abandon_committed")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				transactionalSchemaPending(t, p, b, pid, oid, done)
				if b.PG {
					fixture.LockExec(t, p.Ctx, owner, "ROLLBACK", 0)
				} else {
					// Returning a lease is not physical session abandonment.
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
					transactionalSchemaPending(t, p, b, pid, oid, done)
					if err := pool.Close(); err != nil {
						t.Fatal(err)
					}
				}
				fixture.LimitLockValues(t, await(), b.PG, 7)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_abandon_private'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fixture.LockExec(t, p.Ctx, peer, "INSERT INTO tx_abandon_committed DEFAULT VALUES", 1)
				rows, err := peer.QueryContext(p.Ctx, "SELECT v FROM tx_abandon_committed ORDER BY v")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, b.PG, 7, 7)
			})
		}
	})
}
