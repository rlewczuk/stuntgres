package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// The complete sequential settings-local smoke schedule, retaining its Go Tx,
// Conn QueryRow, held Prepare, notice order and rollback recovery routes.
func TestReferenceSettingsMigratedLocalSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "local_path")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, a, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			exec := func(x fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, x, record, fixture.Exec(q, 0)) }
			read := func(x interface {
				QueryRowContext(context.Context, string, ...any) *sql.Row
			}, q, want string) {
				var value string
				if err := x.QueryRowContext(p.Ctx, q).Scan(&value); err != nil || value != want {
					t.Fatal(q, value, want, err)
				}
			}
			begin := func() *sql.Tx {
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { tx.Rollback() })
				return tx
			}
			tx := begin()
			exec(tx, "SET SESSION application_name='s'")
			read(tx, "SHOW application_name", "s")
			fixture.State(t, tx.Commit(), "")
			read(a, "SHOW application_name", "s")
			for _, commit := range []bool{false, true} {
				tx = begin()
				exec(tx, "SET application_name='candidate'")
				exec(tx, "SET LOCAL application_name='local'")
				read(tx, "SHOW application_name", "local")
				want := "s"
				if commit {
					fixture.State(t, tx.Commit(), "")
					want = "candidate"
				} else {
					fixture.State(t, tx.Rollback(), "")
				}
				read(a, "SHOW application_name", want)
			}
			for _, local := range []string{"DEFAULT", "''"} {
				tx = begin()
				exec(tx, "SET application_name='s'")
				exec(tx, "SET LOCAL application_name="+local)
				exec(tx, "RESET ALL")
				read(tx, "SHOW application_name", "")
				fixture.State(t, tx.Commit(), "")
				want := "s"
				if local == "''" {
					want = ""
				}
				read(a, "SHOW application_name", want)
			}
			tx = begin()
			read(tx, "SELECT set_config('application_name','transient',true)", "transient")
			read(tx, "SELECT current_setting('application_name')", "transient")
			fixture.State(t, tx.Commit(), "")
			read(a, "SHOW application_name", "")
			before := len(notices)
			read(a, "SELECT set_config('application_name','outside',true)", "outside")
			read(a, "SHOW application_name", "")
			if len(notices) != before {
				t.Fatal("function warned", notices[before:])
			}
			exec(a, "CREATE SCHEMA local_path")
			exec(a, "CREATE TABLE local_path.t(v TEXT)")
			fixture.RunStep(t, p.Ctx, a, record, fixture.Exec("INSERT INTO local_path.t VALUES('local')", 1))
			tx = begin()
			exec(tx, "SET LOCAL SCHEMA 'local_path'")
			read(tx, "SELECT current_schema", "local_path")
			read(tx, "SELECT v FROM t", "local")
			fixture.State(t, tx.Rollback(), "")
			read(a, "SELECT current_schema", "public")
			before = len(notices)
			exec(a, "SET LOCAL application_name='ignored'")
			read(a, "SHOW application_name", "")
			warning := fixture.ControlsReferenceDiagnostic("25P01", "SET LOCAL can only be used in transaction blocks")
			warning.Severity = "WARNING"
			warning.SeverityUnlocalized = "WARNING"
			if len(notices) != before+1 || notices[before] != warning {
				t.Fatal(notices)
			}
			_, err = a.ExecContext(p.Ctx, "SET LOCAL client_min_messages='bogus'")
			want := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "client_min_messages": "bogus"`)
			want.Hint = "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."
			fixture.AssertDiagnostic(t, err, &want)
			if len(notices) != before+2 || notices[before+1] != warning {
				t.Fatal("warning missing before error", notices)
			}
			stmt, err := a.PrepareContext(p.Ctx, "SET LOCAL smoke.implicit='x'")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			before = len(notices)
			_, err = a.ExecContext(p.Ctx, "SHOW smoke.implicit")
			want = fixture.ControlsReferenceDiagnostic("42704", `unrecognized configuration parameter "smoke.implicit"`)
			fixture.AssertDiagnostic(t, err, &want)
			if len(notices) != before {
				t.Fatal("Prepare warned")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "SET LOCAL smoke.implicit='x'"}, record, fixture.Exec("SET LOCAL smoke.implicit='x'", 0))
			stmt.Close()
			read(a, "SHOW smoke.implicit", "")
			_, err = b.ExecContext(p.Ctx, "SHOW smoke.implicit")
			fixture.AssertDiagnostic(t, err, &want)
			tx = begin()
			exec(tx, "SET application_name='s'")
			exec(tx, "SET LOCAL application_name='l'")
			_, err = tx.ExecContext(p.Ctx, "SET absent_setting='x'")
			want = fixture.ControlsReferenceDiagnostic("42704", `unrecognized configuration parameter "absent_setting"`)
			fixture.AssertDiagnostic(t, err, &want)
			_, err = tx.ExecContext(p.Ctx, "SHOW application_name")
			want = fixture.TransactionAborted()
			fixture.AssertDiagnostic(t, err, &want)
			fixture.State(t, tx.Rollback(), "")
			read(a, "SHOW application_name", "")
			exec(a, "SET application_name='a'")
			read(b, "SHOW application_name", "")
			fixture.RunStep(t, p.Ctx, a, record, fixture.Prepared(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"a"}})))
		})
	}
}
