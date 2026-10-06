//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func referenceSequencePreparedDemand(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s') FROM anchor", "SELECT nextval('s') FROM anchor WHERE v=2", "SELECT nextval(NULL)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)", "SELECT setval('s',nextval('t'),NULL)", "SELECT setval('s',nextval('t'),flag) FROM runtime_null"} {
		t.Run("Undemanded/"+query, func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}} {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					for _, q := range []string{"CREATE TABLE anchor(v INT)", "CREATE TABLE runtime_null(flag BOOL)", "CREATE SEQUENCE s START 10", "CREATE SEQUENCE t START 20"} {
						sequenceProbeExec(t, p.ctx, a, q)
					}
					referenceLockExec(t, p.ctx, a, "INSERT INTO runtime_null VALUES(NULL)", 1)
					if query == "SELECT nextval('s') FROM anchor WHERE v=2" {
						referenceLockExec(t, p.ctx, a, "INSERT INTO anchor VALUES(1)", 1)
					}
					if query == "SELECT setval('s',nextval('t'),flag) FROM runtime_null" {
						sequenceProbeValue(t, p.ctx, a, "SELECT nextval('t')", 20)
					}
					held, err := b.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					sequenceProbeExec(t, p.ctx, b, "BEGIN")
					active := true
					defer func() {
						if active {
							b.ExecContext(p.ctx, "ROLLBACK")
						}
					}()
					rows, err := held.QueryContext(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
						rows.Close()
						t.Fatal(types)
					}
					if query == "SELECT nextval('s') FROM anchor" || query == "SELECT nextval('s') FROM anchor WHERE v=2" {
						if rows.Next() {
							rows.Close()
							t.Fatal("zero-row target was evaluated")
						}
					} else {
						if !rows.Next() {
							rows.Close()
							t.Fatal(rows.Err())
						}
						var value sql.NullInt64
						if err := rows.Scan(&value); err != nil || value.Valid {
							rows.Close()
							t.Fatal("strict NULL result", value, err)
						}
						if rows.Next() {
							rows.Close()
							t.Fatal("extra result row")
						}
					}
					if err := rows.Err(); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					rows.Close()
					// Keep the statement's explicit transaction alive: a sequence RX
					// would make this independent peer ALTER fail its bounded context.
					ctx, cancel := context.WithTimeout(p.ctx, time.Second)
					_, err = a.ExecContext(ctx, "ALTER SEQUENCE s CACHE 1")
					cancel()
					if err != nil {
						t.Fatal("undemanded target opened/locked s", err)
					}
					sequenceProbeExec(t, p.ctx, b, "ROLLBACK")
					active = false
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 10)
					if query == "SELECT setval('s',nextval('t'),flag) FROM runtime_null" {
						sequenceProbeValue(t, p.ctx, b, "SELECT currval('t')", 21)
						sequenceProbeValue(t, p.ctx, a, "SELECT nextval('t')", 22)
					} else {
						var value int64
						err := b.QueryRowContext(p.ctx, "SELECT currval('t')").Scan(&value)
						want := controlsReferenceDiagnostic("55000", `currval of sequence "t" is not yet defined in this session`)
						referenceAssertDiagnostic(t, err, &want)
						sequenceProbeValue(t, p.ctx, a, "SELECT nextval('t')", 20)
					}
				})
			}
		})
	}
}

func referenceSequencePreparedQueuedArguments(t *testing.T) {
	t.Run("QueuedArguments", func(t *testing.T) {
		p := newReferencePair(t)
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s START 10")
		sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE t START 20")
		var oid int64
		if err := a.QueryRowContext(p.ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		held, err := b.PrepareContext(p.ctx, "SELECT setval('s',nextval('t'))")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceLockExec(t, p.ctx, tx, "ALTER SEQUENCE s RENAME TO identity", 0)
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			rows, err := held.QueryContext(ctx)
			return relationOutcome{rows: rows, err: err}
		})
		referenceSequenceWaitBehind(t, p.ctx, bp, ap, oid)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		outcome := await()
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		referenceSequenceRows(t, outcome.rows, "setval", 20)
		sequenceProbeValue(t, p.ctx, b, "SELECT currval('t')", 20)
		sequenceProbeValue(t, p.ctx, b, "SELECT lastval()", 20)
		var value int64
		err = held.QueryRowContext(p.ctx).Scan(&value)
		want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
		want.Position = "15"
		referenceAssertDiagnostic(t, err, &want)
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('identity')", 21)
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('t')", 21)
	})
}
