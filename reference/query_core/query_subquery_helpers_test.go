package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// subqueryReference uses the existing fixture and one pinned-session scenario body.
func subqueryReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
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

func subqueryRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func subqueryBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func subqueryReductionFixture(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE r(v INT)", 0))
	run(fixture.Exec("INSERT INTO r VALUES(1),(2),(NULL),(2)", 4))
}

func subqueryScopeFixture(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE outer_t(id INT)", 0))
	run(fixture.Exec("INSERT INTO outer_t VALUES(1),(2),(3),(3)", 4))
	run(fixture.Exec("CREATE TABLE inner_t(k INT,v INT)", 0))
	run(fixture.Exec("INSERT INTO inner_t VALUES(1,10),(1,11),(2,NULL)", 3))
}

func subqueryCardinality() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("21000", "more than one row returned by a subquery used as an expression")
}

// PostgreSQL may fail while streaming; native must fail before publication.
// The fixture's ordinary Query error runner expects initial Query failure, so
// this callback consumes the actual cursor before checking the complete record.
func subqueryQueryFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q, args...)
	if rows != nil {
		defer rows.Close()
		if !b.PG {
			t.Fatal("native failed query exposed a cursor")
		}
		for rows.Next() {
		}
		if err == nil {
			err = rows.Err()
		}
	}
	fixture.AssertDiagnostic(t, err, &want)
}

func subqueryAnalysisFailure(run func(fixture.Step), q string, want fixture.Diagnostic) {
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, want)
			s.Prepared, s.SelectRows = prepared, queryMode
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			run(s)
		}
	}
}
