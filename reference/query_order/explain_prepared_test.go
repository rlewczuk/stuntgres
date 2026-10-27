package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExplainPreparedAdmission(t *testing.T) {
	t.Run("stored-alias-lifecycle", explainStoredAliasLifecycle)
	t.Run("parameters-catalog-ownership-control", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE explain_held(id integer)", 0))
				run(fixture.Exec("INSERT INTO explain_held VALUES(1),(2)", 2))
				const q = "EXPLAIN SELECT id FROM explain_held WHERE id=$1 ORDER BY id DESC"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var retained, snapshot []string
				for _, arg := range []any{int64(1), int64(2), nil} {
					for _, prepared := range []bool{false, true} {
						lines := explainQuery(t, p, b, q, prepared, arg)
						if b.PG && arg == nil && !strings.Contains(strings.Join(lines, "\n"), "explain_held") {
							// A NULL equality cannot select rows: PostgreSQL may
							// prove that without retaining any relation scan.
							explainContains(t, lines, "rows=0", "One-Time Filter: false")
						} else {
							explainContains(t, lines, "explain_held")
						}
					}
					rows, err := stmt.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					lines := explainTextRows(t, rows)
					if b.PG {
						if arg == nil && !strings.Contains(strings.Join(lines, "\n"), "explain_held") {
							explainContains(t, lines, "rows=0", "One-Time Filter: false")
						} else {
							explainContains(t, lines, "explain_held")
						}
					}
					if !b.PG {
						explainContains(t, lines, "$1", "Filter", "Logical Scan")
					}
					if retained == nil {
						retained = lines
						snapshot = append([]string(nil), lines...)
					}
				}
				if _, err := stmt.QueryContext(p.Ctx); err == nil {
					t.Fatal("missing parameter accepted")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if rows, err := stmt.QueryContext(ctx, int64(1)); !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("pre-canceled query: %v", err)
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(retained, snapshot) {
					t.Fatalf("retained text changed: %v, want %v", retained, snapshot)
				}
				// Same-name replacement must bind the current catalog, not retain
				// a closed statement's old source graph.
				held, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				run(fixture.Exec("ALTER TABLE explain_held RENAME TO explain_old", 0))
				run(fixture.Exec("CREATE TABLE explain_held(id integer)", 0))
				run(fixture.Exec("INSERT INTO explain_held VALUES(9)", 1))
				rows, err := held.QueryContext(p.Ctx, int64(9))
				if err != nil {
					t.Fatal(err)
				}
				explainContains(t, explainTextRows(t, rows), "explain_held")
				run(fixture.Select("SELECT id FROM explain_old ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(fixture.Select("SELECT id FROM explain_held ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
				if !b.PG {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("CREATE TABLE explain_held(id integer)", 0))
					rows, err := held.QueryContext(p.Ctx, int64(9))
					if err != nil {
						t.Fatal(err)
					}
					explainContains(t, explainTextRows(t, rows), "explain_held")
					run(fixture.Select("SELECT id FROM explain_held", []string{"id"}, []string{"INT4"}, nil))
				}
				if !reflect.DeepEqual(retained, snapshot) {
					t.Fatal("retained plan changed after catalog/control invalidation")
				}
			})
		}
	})
	t.Run("read-only-and-rowlocks", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE explain_lock(id integer PRIMARY KEY,v integer)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO explain_lock VALUES(1,10)", 1))
				tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, q := range []string{"EXPLAIN INSERT INTO explain_lock VALUES(2,20)", "EXPLAIN UPDATE explain_lock SET v=99", "EXPLAIN DELETE FROM explain_lock", "EXPLAIN MERGE INTO explain_lock t USING (SELECT 1 AS id) s ON t.id=s.id WHEN MATCHED THEN DELETE"} {
					for _, prepared := range []bool{false, true} {
						rows := mustExplainCapabilityRows(t, p, tx, q, prepared)
						explainContains(t, explainTextRows(t, rows), "explain_lock")
					}
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				locker, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer locker.Rollback()
				for _, prepared := range []bool{false, true} {
					rows := mustExplainCapabilityRows(t, p, locker, "EXPLAIN SELECT id FROM explain_lock FOR UPDATE", prepared)
					explainContains(t, explainTextRows(t, rows), "explain_lock")
				}
				// A second session's NOWAIT proves no tuple lock was acquired;
				// the first transaction remains open throughout, without sleeps.
				other, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Rollback()
				fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Select("SELECT id FROM explain_lock FOR UPDATE NOWAIT", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Exec("UPDATE explain_lock SET v=11 WHERE id=1", 1))
				if err := other.Rollback(); err != nil {
					t.Fatal(err)
				}
				if err := locker.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM explain_lock", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			})
		}
	})
	t.Run("current-authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"explain_reader"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE explain_reader", 0))
				run(fixture.Exec("CREATE TABLE explain_auth(id integer)", 0))
				run(fixture.Exec("INSERT INTO explain_auth VALUES(1)", 1))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO explain_reader", 0))
				run(fixture.Exec("GRANT SELECT ON explain_auth TO explain_reader", 0))
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION explain_reader"); err != nil {
					t.Fatal(err)
				}
				defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				const q = "EXPLAIN SELECT id FROM explain_auth"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				explainContains(t, explainTextRows(t, rows), "explain_auth")
				run(fixture.Exec("REVOKE SELECT ON explain_auth FROM explain_reader", 0))
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table explain_auth")
				held := fixture.HeldStatement{conn, stmt, q}
				s := fixture.ErrorDiagnostic(q, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				for _, prepared := range []bool{false, true} {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
				run(fixture.Exec("GRANT SELECT ON explain_auth TO explain_reader", 0))
				rows, err = stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				text := strings.Join(explainTextRows(t, rows), "\n")
				if !strings.Contains(text, "explain_auth") {
					t.Fatal(text)
				}
			})
		}
	})
}
