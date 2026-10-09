//go:build reference

package reference_test

import (
	"context"
	"testing"
)

func TestReferenceCheckPrivilegeDetailPostgres(t *testing.T) {
	for _, mode := range []string{"none", "readable", "columns", "table"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
			run(referenceExec("CREATE ROLE check_actor", 0))
			t.Cleanup(func() {
				if _, err := referenceAdmin.ExecContext(context.Background(), "DROP OWNED BY check_actor; DROP ROLE check_actor"); err != nil {
					t.Error(err)
				}
			})
			run(referenceExec("CREATE TABLE t(n INT,secret TEXT,readable TEXT,CONSTRAINT c CHECK(n=1 AND secret='private'))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'private','visible')", 1))
			run(referenceExec("GRANT UPDATE(n) ON t TO check_actor", 0))
			run(referenceExec("GRANT USAGE ON SCHEMA public TO check_actor", 0))
			detail := "n) = (2"
			switch mode {
			case "readable":
				run(referenceExec("GRANT SELECT(readable) ON t TO check_actor", 0))
				detail = "n, readable) = (2, visible"
			case "columns":
				run(referenceExec("GRANT SELECT(n,secret,readable) ON t TO check_actor", 0))
				detail = "n, secret, readable) = (2, private, visible"
			case "table":
				run(referenceExec("GRANT SELECT ON t TO check_actor", 0))
				detail = "2, private, visible"
			}
			run(referenceExec("SET ROLE check_actor", 0))
			run(referenceImplicitError("UPDATE t SET n=2", referenceCheckViolation("t", "c", detail)))
			run(referenceExec("RESET ROLE", 0))
			run(referenceSelect("SELECT * FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "private", "visible"}}))
		})
	}
}
func TestReferenceCheckTableoidBoundary(t *testing.T) {
	p := newReferencePair(t)
	t.Run("postgres_supported", func(t *testing.T) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t(a INT CHECK(tableoid>0))", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})} {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		}
	})
	t.Run("native_existing_system_column_boundary", func(t *testing.T) {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE t(a INT)", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceImplicitError("SELECT tableoid FROM t", referenceDiagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"}))
	})
}
