package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"add_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		var notices []fixture.Diagnostic
		_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,3)", 1))
		run(fixture.Exec("CREATE USER add_reader", 0))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO add_reader", 0))
		}
		defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION add_reader", 0))
		run(fixture.ImplicitError("ALTER TABLE t ADD IF NOT EXISTS b nonexistent_type DEFAULT 'oops'", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		if len(notices) != 0 {
			t.Fatal("unauthorized skip notice", notices)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
		run(fixture.ImplicitError("ALTER TABLE missing ADD c INT DEFAULT 'oops'", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}))
		for _, target := range []struct{ name, kind, ddl string }{
			{"v", "views", "CREATE VIEW v AS SELECT id FROM t"},
			{"s", "sequences", "CREATE SEQUENCE s"},
			{"idx", "indexes", "CREATE INDEX idx ON t(id)"},
		} {
			run(fixture.Exec(target.ddl, 0))
			run(fixture.ImplicitError("ALTER TABLE "+target.name+" ADD c INT DEFAULT 'oops'", fixture.Diagnostic{Code: "42809", Message: fmt.Sprintf(`ALTER action ADD COLUMN cannot be performed on relation "%s"`, target.name), Detail: "This operation is not supported for " + target.kind + "."}))
		}
	}
}
