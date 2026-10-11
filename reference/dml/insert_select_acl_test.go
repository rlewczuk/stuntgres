package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectIndependentACL(t *testing.T) {
	const writer = "insert_select_writer"
	if *fixture.Mode {
		fixture.RoleReferenceCleanup(t, []string{writer})
	}
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	p.Step(t, fixture.Exec("CREATE ROLE "+writer+" LOGIN", 0))
	if p.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			admin := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
			admin(fixture.Exec("CREATE TABLE source(i INT)", 0))
			admin(fixture.Exec("CREATE TABLE denied_target(i INT)", 0))
			admin(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			var actor fixture.Executor
			if backend.PG {
				conn, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				fixture.InsertSelectRun(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+writer, 0))
				actor = conn
			} else {
				d := p.Native.Driver().(*stuntgres.Driver)
				connector, err := d.OpenConnectorAs(dsn, writer)
				if err != nil {
					t.Fatal(err)
				}
				wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector}, fixture.Interceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(configured)
				defer db.Close()
				actor = db
			}
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, actor, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE target(i INT)", 0))
			run(fixture.Exec("CREATE TABLE own_source(i INT)", 0))
			run(fixture.Exec("INSERT INTO own_source VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, table string }{
					{"INSERT INTO target SELECT i FROM source", "source"},
					{"INSERT INTO target SELECT 1 FROM source WHERE FALSE", "source"},
					{"INSERT INTO denied_target SELECT i FROM own_source", "denied_target"},
					{"INSERT INTO target SELECT 1 FROM source", "source"},
				} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + tc.table})
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				s := fixture.Exec("INSERT INTO target SELECT i FROM own_source", 1)
				s.Prepared = prepared
				run(s)
			}
			rows := fixture.Select("SELECT i FROM target", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
			rows.Bag = true
			run(rows)
			admin(rows)
		})
	}
}
