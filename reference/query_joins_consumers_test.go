package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceQueryJoinsInsertAtomicityAndDefaults(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE dst(k INT NOT NULL,stamp INT DEFAULT 7)", 0))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			insert := referenceExec("INSERT INTO dst(k) SELECT k FROM a JOIN b USING(k)", 4)
			insert.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, insert)
			for _, failure := range []struct {
				q    string
				want referenceDiagnostic
			}{
				{"INSERT INTO dst(k) SELECT 1/(b.k-3) FROM a CROSS JOIN b", referenceDiagnostic{Code: "22012", Message: "division by zero"}},
				{"INSERT INTO dst(k) SELECT a.k FROM a CROSS JOIN b", referenceDiagnostic{Code: "23502", Message: `null value in column "k" of relation "dst" violates not-null constraint`, Detail: "Failing row contains (null, 7).", Schema: "public", Table: "dst", Column: "k"}},
			} {
				queryJoinsScalarFailure(t, p, backend, failure.q, failure.want, prepared, true)
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err := backend.db.ExecContext(ctx, insert.query)
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("context error gained SQLSTATE", err)
			}
			want := int64(4)
			if prepared {
				want = 8
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT count(*) FROM dst WHERE k=1 AND stamp=7", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
		}
	}
}

func TestReferenceQueryJoinsEmptyAggregateAdmission(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, step := range []referenceStep{
		referenceSelect("SELECT count(*) FROM a JOIN empty USING(k)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT k,count(*) FROM a JOIN empty USING(k) GROUP BY k", []string{"k", "count"}, []string{"INT4", "INT8"}, nil),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
