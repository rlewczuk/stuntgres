package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceSequencePreparedDemand(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s') FROM anchor", "SELECT nextval('s') FROM anchor WHERE v=2", "SELECT nextval(NULL)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)", "SELECT setval('s',nextval('t'),NULL)", "SELECT setval('s',nextval('t'),flag) FROM runtime_null"} {
		t.Run("Undemanded/"+query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
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
					rows, err := held.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
					if err != nil {
						t.Fatal(err)
					}
					label := "setval"
					expected := [][]any{{nil}}
					if query == "SELECT nextval('s') FROM anchor" || query == "SELECT nextval('s') FROM anchor WHERE v=2" {
						label = "nextval"
						expected = [][]any{}
					}
					if query == "SELECT nextval(NULL)" {
						label = "nextval"
					}
					columns, columnErr := rows.Columns()
					if columnErr != nil || len(columns) != 1 || columns[0] != label {
						rows.Close()
						t.Fatal(columns, columnErr)
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
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					sequencePreparedRecorded(t, backend, referenceStep{query: query, columns: []string{label}, types: []string{"INT8"}, rows: expected})
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

func TestReferenceSequencePreparedQueuedArguments(t *testing.T) {
	t.Run("QueuedArguments", func(t *testing.T) {
		p := newReferenceScenario(t)
		if p.postgres == nil {
			return
		} // O: real queue, PID and original OID.
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
		ap, bp := sequencePreparedPID(t, p, a), sequencePreparedPID(t, p, b)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceLockExec(t, p.ctx, tx, "ALTER SEQUENCE s RENAME TO identity", 0)
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			rows, err := held.QueryContext(context.WithValue(ctx, referenceRecorderKey{}, p.postgresRecord))
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
		sequencePreparedRecorded(t, referenceBackend{recorder: p.postgresRecord, pg: true}, referenceStep{query: "SELECT setval('s',nextval('t'))", columns: []string{"setval"}, types: []string{"INT8"}, rows: [][]any{{int64(20)}}})
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
