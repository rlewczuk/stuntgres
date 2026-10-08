//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterTypeViewDependencies(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"where", "SELECT b FROM t WHERE a=1"},
		{"order", "SELECT b FROM t ORDER BY a"},
		{"join", "SELECT t.b FROM t JOIN u ON t.a=u.c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, b := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,7)", 1))
					run(referenceExec("CREATE TABLE u(c INT)", 0))
					run(referenceExec("INSERT INTO u VALUES(1)", 1))
					run(referenceExec("CREATE VIEW v AS "+tc.query, 0))
					run(referenceImplicitError("ALTER TABLE t ALTER a TYPE BIGINT", referenceDiagnostic{Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view v depends on column "a"`}))
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
					run(referenceSelect("SELECT * FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
