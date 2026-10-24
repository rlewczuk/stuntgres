package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func citextComparisonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

var citextComparisonNames = []string{"eq", "ne", "lt", "le", "gt", "ge"}
var citextComparisonTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}

func citextComparisonProjection(a, b string) string {
	return a + " = " + b + " AS eq," + a + " <> " + b + " AS ne," + a + " < " + b + " AS lt," + a + " <= " + b + " AS le," + a + " > " + b + " AS gt," + a + " >= " + b + " AS ge"
}

func citextComparisonRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func citextComparisonCheckDiagnostic() fixture.Diagnostic {
	d := citextDiagnostic("23514", `new row for relation "citext_compare_guard" violates check constraint "citext_compare_equal"`)
	d.Detail = "Failing row contains (3, ABC, abd)."
	d.Schema, d.Table, d.Constraint = "public", "citext_compare_guard", "citext_compare_equal"
	return d
}
