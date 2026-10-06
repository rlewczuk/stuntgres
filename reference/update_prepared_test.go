//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateCoreRetainedPrepared(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"memory", "postgres"}[i], func(t *testing.T) {
			record := p.memoryRecord
			if i == 1 {
				record = p.postgresRecord
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
			run(referenceExec("CREATE TABLE t(id INT,a INT NOT NULL,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,10,20),(2,30,40)", 2))
			stmt, err := db.PrepareContext(p.ctx, "UPDATE t SET a=$1,b=a WHERE id=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{int64(7), int64(1)}, {int64(9), int64(1)}, {int64(5), int64(2)}} {
				r, err := stmt.ExecContext(p.ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, r, 1)
			}
			run(referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}}))
			r, err := stmt.ExecContext(p.ctx, 6, 99)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, r, 0)
			_, err = stmt.ExecContext(p.ctx, nil, 2)
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, null, 5).", Schema: "public", Table: "t", Column: "a"}
			referenceAssertDiagnostic(t, err, &want)
			run(referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}}))
		})
	}
}
