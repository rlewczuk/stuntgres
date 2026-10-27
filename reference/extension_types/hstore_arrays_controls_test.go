package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hstoreArraysControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	for _, input := range []string{"v"} {
		q := hstoreArraysQuery(input) + " FROM hs_controls WHERE id<=5 ORDER BY id"
		want := [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}, {"{}", "{}", "{}", "{}"}, {nil, nil, nil, nil}, {"{a,NULL}", "{{a,NULL}}", "{a,NULL}", "{{a,NULL}}"}, {`{a,"NULL"}`, `{{a,"NULL"}}`, `{a,"NULL"}`, `{{a,"NULL"}}`}}
		s := hstoreArraysSelect(b, q, want)
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
}
func TestReferenceHstoreArraysControls(t *testing.T) {
	for _, phase := range []string{"same", "fresh", "array_signature", "matrix_kind", "operator_signature", "operator_slot"} {
		t.Run(phase, func(t *testing.T) { hstoreControlPhase(t, phase) })
	}
	t.Run("stored_view", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				// Native replay above exercised the emitted dump in this same oracle.
				// Start the ordinary PostgreSQL phase from a clean public schema.
				hstoreRun(t, p, b, c, fixture.Exec("DROP SCHEMA public CASCADE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA public", 0))
			}
			hstoreInstall(t, p, b, c)
			hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_array_view_source(id integer,v hstore)", 0))
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_array_view_source VALUES (1,'a=>1'),(2,''),(3,NULL)", 3))
			hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_array_view AS "+hstoreArraysQuery("v")+" FROM hs_array_view_source WHERE id=1", 0))
			q := "SELECT a,m,u,w FROM hs_array_view"
			want := [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}
			hstoreRun(t, p, b, c, hstoreArraysSelect(b, q, want))
			if b.PG {
				return
			}
			rows, err := b.DB.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, c, hstoreArraysSelect(b, q, want))
			var got [][]any
			for rows.Next() {
				var a, m, u, w any
				if err := rows.Scan(&a, &m, &u, &w); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{a, m, u, w})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("retained array rows %#v want %#v", got, want)
			}
			for _, column := range columns {
				if column.DatabaseTypeName() != "_TEXT" {
					t.Fatal("retained array metadata", column.DatabaseTypeName())
				}
			}
			destination := fixture.NewFixture(t, false, true)
			if _, err := destination.Native.ExecContext(p.Ctx, "CREATE EXTENSION hstore"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
			hstoreRun(t, p, target, destination.Memory, hstoreArraysSelect(target, q, want))
			if p.Postgres != nil {
				pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				hstoreRun(t, p, pg, p.Postgres, hstoreArraysSelect(pg, q, want))
			}
		})
	})
}
func TestReferenceHstoreArraysFeatureSmoke(t *testing.T) {
	t.Run("values_projection_predicates_shape", TestReferenceHstoreArraysValues)
	t.Run("diagnostics_atomicity", TestReferenceHstoreArraysDiagnostics)
	t.Run("current_authority", TestReferenceHstoreArraysPrivileges)
	t.Run("restored_invocation", TestReferenceHstoreArraysControls)
	t.Run("presentation_identity", TestReferenceHstoreArraysPresentation)
}

func TestReferenceHstoreArraysPresentation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_array_namespace")
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_array_namespace", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_array_presentation(v hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_array_presentation VALUES ('a=>1')", 1))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_array_presentation_view AS "+hstoreArraysQuery("v")+" FROM hs_array_presentation", 0))
		q := hstoreArraysQuery("v") + " FROM hs_array_presentation"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		want := [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}
		hstoreRun(t, p, b, held, hstoreArraysSelect(b, q, want))
		operatorQuery := "SELECT %% v AS a,%# v AS m,%% v AS u,%# v AS w FROM hs_array_presentation"
		operatorStmt, err := c.PrepareContext(p.Ctx, operatorQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer operatorStmt.Close()
		operatorHeld := fixture.HeldStatement{Executor: c, Statement: operatorStmt, SQL: operatorQuery}
		hstoreRun(t, p, b, operatorHeld, hstoreArraysSelect(b, operatorQuery, want))
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'hstore_to_array(hstore)'::regprocedure::oid").Scan(&before); err != nil {
			t.Fatal(err)
		}
		hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION hstore_to_array(hstore) RENAME TO hs_array_renamed", 0))
		hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION hs_array_renamed(hstore) SET SCHEMA hs_array_namespace", 0))
		var after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'hs_array_namespace.hs_array_renamed(hstore)'::regprocedure::oid").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("presentation replaced installed identity", before, after)
		}
		missing := fixture.ControlsReferenceDiagnostic("42883", "function hstore_to_array(hstore) does not exist")
		missing.Position = "8"
		missing.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		hstoreError(t, p, b, held, q, false, true, false, missing)
		hstoreRun(t, p, b, operatorHeld, hstoreArraysSelect(b, operatorQuery, want))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, "SELECT hs_array_namespace.hs_array_renamed(v) AS a,hstore_to_matrix(v) AS m,%% v AS u,%# v AS w FROM hs_array_presentation", want))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, "SELECT a,m,u,w FROM hs_array_presentation_view", want))
	})
}
