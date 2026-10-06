//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateCoreBindOccurrenceResolution(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b INT,f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,TRUE)", 1))
	bad := referenceImplicitError("UPDATE t SET a=$1,b=$1", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad")
	bad.errorStage = "execute"
	pg := *bad.diagnostic
	pg.Context = "unnamed portal parameter $1 = '...'"
	bad.postgresDiagnostic = &pg
	p.step(t, bad)
	p.step(t, referencePrepared(bad))
	p.step(t, referenceSelect("SELECT a,b,f FROM t", []string{"a", "b", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(2), true}}))
	for _, tc := range []struct{ q, pos string }{{"UPDATE t SET a=$1,f=$1", "21"}, {"UPDATE /*é*/ t SET a=$1,f=$1", "27"}} {
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: tc.pos}, 1)
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	gap := referenceImplicitError("UPDATE t SET a=$2", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
	gap.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, gap)
	simple := referenceImplicitError(gap.query, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "16"})
	simple.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, simple)
	gap.errorStage = "prepare"
	p.step(t, referencePrepared(gap))
	gap.errorStage = "execute"
	gap.args = []any{nil, 7}
	p.step(t, gap)
	p.step(t, referenceExec(`CREATE SCHEMA "é space"`, 0))
	p.step(t, referenceExec(`CREATE TABLE "é space"."T""x"(id INT,"A" INT,b INT)`, 0))
	p.step(t, referenceExec(`INSERT INTO "é space"."T""x" VALUES(1,10,20),(2,30,40)`, 2))
	q := `UPDATE "é space"."T""x" SET "A"=$1,b="A" WHERE id=$2`
	p.step(t, referenceExec(q, 1, 7, 1))
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		record := p.memoryRecord
		if i == 1 {
			record = p.postgresRecord
		}
		stmt, err := db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]any{{9, 1}, {5, 2}} {
			result, err := stmt.ExecContext(p.ctx, args...)
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
		}
		stmt.Close()
		referenceRunStep(t, p.ctx, db, record, referenceSelect(`SELECT id,"A",b FROM "é space"."T""x" ORDER BY id`, []string{"id", "A", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}}))
	}
	p.step(t, referenceExec("UPDATE t SET a=$1,b=$1", 1, 8))
	p.step(t, referencePrepared(referenceExec("UPDATE t SET a=$1,b=$1", 1, 9)))
	p.step(t, referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
}
