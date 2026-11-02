package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const updateCall = "SELECT dbupd_ext.dblink_build_sql_update($1::text,$2::int2vector,$3::integer,$4::text[],$5::text[]) AS sql"
const updateSignature = "(text,int2vector,integer,text[],text[])"

func updateCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"dbupd_ext", "dbupd_left", "dbupd_right", "dbupd_moved"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS dblink CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				t.Cleanup(clean)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA dbupd_ext", 0))
			if install {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION dblink SCHEMA dbupd_ext", 0))
			}
			body(t, p, b, c)
		})
	}
}

func updateSelect(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"sql"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func updateError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func updateBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Prepared(s))
}

func updateRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) string {
	t.Helper()
	got := fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	return got.Rows[0][0].(string)
}

func updateHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
}

func updateMember(name, schema string) fixture.Step {
	q := "SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND d.objsubid=0 AND d.refobjsubid=0 AND e.extname='dblink' AND n.nspname='" + schema + "' AND p.proname='" + name + "' AND p.proargtypes::text='25 22 23 1009 1009'"
	return fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
}

func updateKeys(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE update_keys(id integer,k text,payload text)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO update_keys VALUES(1,'a','O''Brien'),(2,NULL,NULL)", 2))
}
