package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceDiagnostics(t *testing.T) {
	cases := []struct {
		q string
		d fixture.Diagnostic
	}{
		{"DROP INDEX missing", fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`}},
		{"ALTER INDEX missing SET(fillfactor=9)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{"ALTER INDEX absent_schema.ix RESET(fillfactor)", fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`}},
		{"ALTER INDEX t SET(fillfactor=80)", fixture.Diagnostic{Code: "42809", Message: `"t" is not an index`}},
		{"ALTER INDEX ix SET(fillfactor=9)", fixture.Diagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}},
		{"ALTER INDEX ix SET(fillfactor=9,bogus.x=1)", fixture.Diagnostic{Code: "22023", Message: `unrecognized parameter namespace "bogus"`}},
		{"ALTER INDEX ix RESET(fillfactor=80)", fixture.Diagnostic{Code: "42601", Message: "RESET must not include values for parameters"}},
		{"/* é */ DROP INDEX CONCURRENTLY ix", fixture.Diagnostic{Code: "25001", Message: "DROP INDEX CONCURRENTLY cannot run inside a transaction block"}},
	}
	for _, c := range cases {
		for _, readonly := range []bool{false, true} {
			t.Run(c.q+map[bool]string{false: "/write", true: "/readonly"}[readonly], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run := transactionalIndexRun(t, p, b, a)
						indexTxMaintenanceSeed(run, "set")
						begin := "BEGIN"
						if readonly {
							begin = "BEGIN READ ONLY"
						}
						run(fixture.Exec(begin, 0))
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("SAVEPOINT recover", 0))
						stmt, err := a.PrepareContext(p.Ctx, c.q)
						if err != nil {
							t.Fatal("semantic Prepare must be quiet", err)
						}
						defer stmt.Close()
						d := c.d
						if readonly {
							tag := "ALTER INDEX"
							if c.q == "DROP INDEX missing" || c.q == "/* é */ DROP INDEX CONCURRENTLY ix" {
								tag = "DROP INDEX"
							}
							d = fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"}
						}
						s := fixture.ImplicitError(c.q, d)
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: c.q}, b.Recorder, s)
						run(fixture.ImplicitError("ALTER INDEX ix RESET(fillfactor)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						run(fixture.Exec("ROLLBACK TO recover", 0))
						run(indexTxMaintenanceState("set", false))
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Exec("ALTER INDEX ix SET(fillfactor=80)", 0))
						run(fixture.Exec("ALTER INDEX ix RESET(fillfactor)", 0))
					})
				}
			})
		}
	}
	for _, q := range []string{"DROP INDEX IF EXISTS missing", "ALTER INDEX IF EXISTS missing SET(fillfactor=9)"} {
		t.Run(q, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, a, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					stmt, err := a.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if len(notices) != 0 {
						t.Fatal("Prepare emitted notice", notices)
					}
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}, recorder, fixture.Exec(q, 0))
					message := `relation "missing" does not exist, skipping`
					if q == "DROP INDEX IF EXISTS missing" {
						message = `index "missing" does not exist, skipping`
					}
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("Execute notices %#v want %#v", notices, want)
					}
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("ROLLBACK", 0))
				})
			}
		})
	}
}

func TestReferenceTransactionalIndexMaintenancePreparedAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"imtx_denied"})
	fixture.SchemaReferenceCleanup(t, "imtx_a")
	fixture.SchemaReferenceCleanup(t, "imtx_b")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalIndexRun(t, p, b, a)
			for _, q := range []string{"CREATE SCHEMA imtx_a", "CREATE SCHEMA imtx_b", "CREATE TABLE imtx_a.t(n INT)", "CREATE TABLE imtx_b.t(n INT)", "CREATE USER imtx_denied", "SET search_path=imtx_a"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("GRANT USAGE ON SCHEMA imtx_b TO imtx_denied", 0))
			q := "ALTER INDEX ix SET(fillfactor=80)"
			stmt, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "ix" does not exist`}))
			for _, q := range []string{"CREATE INDEX ix ON imtx_a.t(n)", "CREATE INDEX ix ON imtx_b.t(n)", "SET search_path=imtx_b", "BEGIN"} {
				run(fixture.Exec(q, 0))
			}
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT n.nspname,c.reloptions::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='ix' AND n.nspname IN ('imtx_a','imtx_b') ORDER BY n.nspname", []string{"nspname", "reloptions"}, []string{"NAME", "TEXT"}, [][]any{{"imtx_a", nil}, {"imtx_b", "{fillfactor=80}"}}))
			run(fixture.Exec("COMMIT", 0))
			var old int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&old); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"DROP INDEX ix", "CREATE INDEX ix ON imtx_b.t(n)", "BEGIN"} {
				run(fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT oid<>$1::oid AS replaced,reloptions::text FROM pg_class WHERE relname='ix' AND relnamespace='imtx_b'::regnamespace", []string{"replaced", "reloptions"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{fillfactor=80}"}}, old))
			run(fixture.Exec("COMMIT", 0))
			// Held preparation never captures superuser or schema authority.
			run(fixture.Exec("SET ROLE imtx_denied", 0))
			defer a.ExecContext(p.Ctx, "RESET ROLE")
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of index ix"}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE imtx_denied", 0))
			denied, err := a.PrepareContext(p.Ctx, "ALTER INDEX imtx_a.ix RESET(fillfactor)")
			if err != nil {
				t.Fatal(err)
			}
			defer denied.Close()
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: denied, SQL: "ALTER INDEX imtx_a.ix RESET(fillfactor)"}, b.Recorder, fixture.Exec("ALTER INDEX imtx_a.ix RESET(fillfactor)", 0))
			run(fixture.Exec("COMMIT", 0))
		})
	}
}
