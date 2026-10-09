package reference_test

import (
	"database/sql"
	"testing"
)

// subqueryReference uses the existing fixture and one pinned-session scenario body.
func subqueryReference(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func subqueryRun(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) func(referenceStep) {
	t.Helper()
	return func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
}

func subqueryBoth(run func(referenceStep), s referenceStep) {
	run(s)
	run(referencePrepared(s))
}

func subqueryReductionFixture(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE r(v INT)", 0))
	run(referenceExec("INSERT INTO r VALUES(1),(2),(NULL),(2)", 4))
}

func subqueryScopeFixture(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE outer_t(id INT)", 0))
	run(referenceExec("INSERT INTO outer_t VALUES(1),(2),(3),(3)", 4))
	run(referenceExec("CREATE TABLE inner_t(k INT,v INT)", 0))
	run(referenceExec("INSERT INTO inner_t VALUES(1,10),(1,11),(2,NULL)", 3))
}

func subqueryCardinality() referenceDiagnostic {
	return controlsReferenceDiagnostic("21000", "more than one row returned by a subquery used as an expression")
}

// PostgreSQL may fail while streaming; native must fail before publication.
// The fixture's ordinary Query error runner expects initial Query failure, so
// this callback consumes the actual cursor before checking the complete record.
func subqueryQueryFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, want referenceDiagnostic, args ...any) {
	t.Helper()
	rows, err := x.QueryContext(p.ctx, q, args...)
	if rows != nil {
		defer rows.Close()
		if !b.pg {
			t.Fatal("native failed query exposed a cursor")
		}
		for rows.Next() {
		}
		if err == nil {
			err = rows.Err()
		}
	}
	referenceAssertDiagnostic(t, err, &want)
}

func subqueryAnalysisFailure(run func(referenceStep), q string, want referenceDiagnostic) {
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, want)
			s.prepared, s.selectRows = prepared, queryMode
			if prepared {
				s.errorStage = "prepare"
			} else {
				s.errorStage = "execute"
			}
			run(s)
		}
	}
}
