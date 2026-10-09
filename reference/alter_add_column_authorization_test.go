package reference_test

import (
	"context"
	"fmt"
	"testing"
)

func TestReferenceAlterAddColumnAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"add_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		var notices []referenceDiagnostic
		_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
		run(referenceExec("CREATE TABLE t(id INT,b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,3)", 1))
		run(referenceExec("CREATE USER add_reader", 0))
		if backend.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO add_reader", 0))
		}
		defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(referenceExec("SET SESSION AUTHORIZATION add_reader", 0))
		run(referenceImplicitError("ALTER TABLE t ADD IF NOT EXISTS b nonexistent_type DEFAULT 'oops'", referenceDiagnostic{Code: "42501", Message: "must be owner of table t"}))
		if len(notices) != 0 {
			t.Fatal("unauthorized skip notice", notices)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}}))
		run(referenceImplicitError("ALTER TABLE missing ADD c INT DEFAULT 'oops'", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`}))
		for _, target := range []struct{ name, kind, ddl string }{
			{"v", "views", "CREATE VIEW v AS SELECT id FROM t"},
			{"s", "sequences", "CREATE SEQUENCE s"},
			{"idx", "indexes", "CREATE INDEX idx ON t(id)"},
		} {
			run(referenceExec(target.ddl, 0))
			run(referenceImplicitError("ALTER TABLE "+target.name+" ADD c INT DEFAULT 'oops'", referenceDiagnostic{Code: "42809", Message: fmt.Sprintf(`ALTER action ADD COLUMN cannot be performed on relation "%s"`, target.name), Detail: "This operation is not supported for " + target.kind + "."}))
		}
	}
}
