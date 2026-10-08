//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBtreeIndexRegression(t *testing.T) {
	p := newReferencePair(t)
	// Siblings deliberately execute complete independent streams: a native
	// fatal regression cannot prevent the pinned PostgreSQL witness running.
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			bag := func(query string, rows [][]any) referenceStep {
				s := referenceSelect(query, []string{"n", "s"}, []string{"INT4", "TEXT"}, rows)
				s.bag = true
				return s
			}
			steps := []referenceStep{
				referenceExec("CREATE TABLE t(n INTEGER,s TEXT)", 0),
				referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
				referenceExec("CREATE INDEX t_n ON t(n)", 0),
				referencePrepared(referenceExec("CREATE INDEX t_s ON t USING btree(s)", 0)),
				bag("SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}),
				bag("SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}),
				bag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}),
				bag("SELECT n,s FROM t WHERE s>'a'", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}),
				referenceSelect("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}),
				referenceSelect("SELECT n,s FROM t ORDER BY n DESC", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}, {int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {int64(1), "a"}}),
			}
			parameter := bag("SELECT n,s FROM t WHERE n=$1", [][]any{{int64(2), "b"}, {int64(2), "b"}})
			parameter.args = []any{2}
			steps = append(steps, referencePrepared(parameter))
			for _, tc := range []struct{ query, code, message string }{
				{"CREATE INDEX t_n ON t(n)", "42P07", `relation "t_n" already exists`},
				{"CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`},
				{"CREATE INDEX retry ON t(missing)", "42703", `column "missing" does not exist`},
			} {
				s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
				s.errorStage = "execute"
				steps = append(steps, s, referencePrepared(s))
			}
			steps = append(steps, referenceExec("CREATE INDEX retry ON t(n)", 0))
			for _, step := range steps {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
		})
	}
}
