package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"stuntgres"
	"testing"
)

func TestReferenceInsertSelectIndependentACL(t *testing.T) {
	const writer = "insert_select_writer"
	if *referenceMode {
		roleReferenceCleanup(t, []string{writer})
	}
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	p.step(t, referenceExec("CREATE ROLE "+writer+" LOGIN", 0))
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			admin := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
			admin(referenceExec("CREATE TABLE source(i INT)", 0))
			admin(referenceExec("CREATE TABLE denied_target(i INT)", 0))
			admin(referenceExec("INSERT INTO source VALUES(1)", 1))
			var actor referenceExecutor
			if backend.pg {
				conn, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				insertSelectRun(t, p.ctx, conn, backend.recorder, referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
				actor = conn
			} else {
				d := p.native.Driver().(*stuntgres.Driver)
				connector, err := d.OpenConnectorAs(dsn, writer)
				if err != nil {
					t.Fatal(err)
				}
				wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(configured)
				defer db.Close()
				actor = db
			}
			run := func(s referenceStep) { insertSelectRun(t, p.ctx, actor, backend.recorder, s) }
			run(referenceExec("CREATE TABLE target(i INT)", 0))
			run(referenceExec("CREATE TABLE own_source(i INT)", 0))
			run(referenceExec("INSERT INTO own_source VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, table string }{
					{"INSERT INTO target SELECT i FROM source", "source"},
					{"INSERT INTO target SELECT 1 FROM source WHERE FALSE", "source"},
					{"INSERT INTO denied_target SELECT i FROM own_source", "denied_target"},
					{"INSERT INTO target SELECT 1 FROM source", "source"},
				} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + tc.table})
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
				}
				s := referenceExec("INSERT INTO target SELECT i FROM own_source", 1)
				s.prepared = prepared
				run(s)
			}
			rows := referenceSelect("SELECT i FROM target", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
			rows.bag = true
			run(rows)
			admin(rows)
		})
	}
}
