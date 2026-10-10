package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceCheckInsertPrivilegeDetailPostgres(t *testing.T) {
	for _, mode := range []string{"none", "readable", "columns", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+strconv.FormatBool(prepared), func(t *testing.T) {
				roleReferenceCleanup(t, []string{"check_actor"})
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
				run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
				run(referenceExec("CREATE ROLE check_actor", 0))
				run(referenceExec("CREATE TABLE t(n INT,secret TEXT DEFAULT 'private',readable TEXT DEFAULT 'visible',CONSTRAINT c CHECK(n=1 AND secret='private'))", 0))
				if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO check_actor", 0)) }
				run(referenceExec("GRANT INSERT(n) ON t TO check_actor", 0))
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
				step := referenceImplicitError("INSERT INTO t(n) VALUES($1)", referenceCheckViolation("t", "c", detail), 2)
				step.prepared = prepared
				run(step)
				run(referenceExec("RESET ROLE", 0))
				run(referenceSelect("SELECT * FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{}))
				}
			})
		}
	}
}
