package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequencePreparedDemand(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s') FROM anchor", "SELECT nextval('s') FROM anchor WHERE v=2", "SELECT nextval(NULL)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)", "SELECT setval('s',nextval('t'),NULL)", "SELECT setval('s',nextval('t'),flag) FROM runtime_null"} {
		t.Run("Undemanded/"+query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					for _, q := range []string{"CREATE TABLE anchor(v INT)", "CREATE TABLE runtime_null(flag BOOL)", "CREATE SEQUENCE s START 10", "CREATE SEQUENCE t START 20"} {
						fixture.SequenceProbeExec(t, p.Ctx, a, q)
					}
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO runtime_null VALUES(NULL)", 1)
					if query == "SELECT nextval('s') FROM anchor WHERE v=2" {
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO anchor VALUES(1)", 1)
					}
					if query == "SELECT setval('s',nextval('t'),flag) FROM runtime_null" {
						fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('t')", 20)
					}
					held, err := b.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					fixture.SequenceProbeExec(t, p.Ctx, b, "BEGIN")
					active := true
					defer func() {
						if active {
							b.ExecContext(p.Ctx, "ROLLBACK")
						}
					}()
					rows, err := held.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
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
					sequencePreparedRecorded(t, backend, fixture.Step{Query: query, Columns: []string{label}, Types: []string{"INT8"}, Rows: expected})
					// Keep the statement's explicit transaction alive: a sequence RX
					// would make this independent peer ALTER fail its bounded context.
					ctx, cancel := context.WithTimeout(p.Ctx, time.Second)
					_, err = a.ExecContext(ctx, "ALTER SEQUENCE s CACHE 1")
					cancel()
					if err != nil {
						t.Fatal("undemanded target opened/locked s", err)
					}
					fixture.SequenceProbeExec(t, p.Ctx, b, "ROLLBACK")
					active = false
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 10)
					if query == "SELECT setval('s',nextval('t'),flag) FROM runtime_null" {
						fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT currval('t')", 21)
						fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('t')", 22)
					} else {
						var value int64
						err := b.QueryRowContext(p.Ctx, "SELECT currval('t')").Scan(&value)
						want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "t" is not yet defined in this session`)
						fixture.AssertDiagnostic(t, err, &want)
						fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('t')", 20)
					}
				})
			}
		})
	}
}

func TestReferenceSequencePreparedQueuedArguments(t *testing.T) {
	t.Run("QueuedArguments", func(t *testing.T) {
		p := fixture.NewScenario(t)
		if p.Postgres == nil {
			return
		} // O: real queue, PID and original OID.
		a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
		fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s START 10")
		fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE t START 20")
		var oid int64
		if err := a.QueryRowContext(p.Ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		held, err := b.PrepareContext(p.Ctx, "SELECT setval('s',nextval('t'))")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ap, bp := sequencePreparedPID(t, p, a), sequencePreparedPID(t, p, b)
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.LockExec(t, p.Ctx, tx, "ALTER SEQUENCE s RENAME TO identity", 0)
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := held.QueryContext(context.WithValue(ctx, fixture.RecorderKey{}, p.PostgresRecord))
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		fixture.SequenceWaitBehind(t, p.Ctx, bp, ap, oid)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		outcome := await()
		if outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
		fixture.SequenceRows(t, outcome.Rows, "setval", 20)
		sequencePreparedRecorded(t, fixture.Backend{Recorder: p.PostgresRecord, PG: true}, fixture.Step{Query: "SELECT setval('s',nextval('t'))", Columns: []string{"setval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(20)}}})
		fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT currval('t')", 20)
		fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT lastval()", 20)
		var value int64
		err = held.QueryRowContext(p.Ctx).Scan(&value)
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
		want.Position = "15"
		fixture.AssertDiagnostic(t, err, &want)
		fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('identity')", 21)
		fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('t')", 21)
	})
}
