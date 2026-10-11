package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryJoinsInsertAtomicityAndDefaults(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	p.Step(t, fixture.Exec("CREATE TABLE dst(k INT NOT NULL,stamp INT DEFAULT 7)", 0))
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			insert := fixture.Exec("INSERT INTO dst(k) SELECT k FROM a JOIN b USING(k)", 4)
			insert.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, insert)
			for _, failure := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"INSERT INTO dst(k) SELECT 1/(b.k-3) FROM a CROSS JOIN b", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
				{"INSERT INTO dst(k) SELECT a.k FROM a CROSS JOIN b", fixture.Diagnostic{Code: "23502", Message: `null value in column "k" of relation "dst" violates not-null constraint`, Detail: "Failing row contains (null, 7).", Schema: "public", Table: "dst", Column: "k"}},
			} {
				queryJoinsScalarFailure(t, p, backend, failure.q, failure.want, prepared, true)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := backend.DB.ExecContext(ctx, insert.Query)
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
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT count(*) FROM dst WHERE k=1 AND stamp=7", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
		}
	}
}

func TestReferenceQueryJoinsEmptyAggregateAdmission(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, step := range []fixture.Step{
		fixture.Select("SELECT count(*) FROM a JOIN empty USING(k)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT k,count(*) FROM a JOIN empty USING(k) GROUP BY k", []string{"k", "count"}, []string{"INT4", "INT8"}, nil),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
