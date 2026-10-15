package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hypotheticalScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, c, b)
		})
	}
}

func hypotheticalProjection(direct, order, filter string) string {
	parts := make([]string, 0, 4)
	for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
		parts = append(parts, name+"("+direct+") WITHIN GROUP(ORDER BY "+order+")"+filter)
	}
	return strings.Join(parts, ",")
}

func hypotheticalResult(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"rank", "dense_rank", "percent_rank", "cume_dist"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, rows)
}

func hypotheticalRow(rank, dense int64, percent, cume float64) []any {
	return []any{rank, dense, percent, cume}
}

func hypotheticalSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_input(v INT)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_input VALUES(10),(20),(20),(40)", 4))
}

func hypotheticalArity(q, name, signature string, direct, order int) fixture.Step {
	return orderedSetDiagnostic(q, "42883", "function "+name+"("+signature+") does not exist", name,
		"To use the hypothetical-set aggregate "+name+", the number of hypothetical direct arguments (here "+strconv.Itoa(direct)+") must match the number of ordering columns (here "+strconv.Itoa(order)+").")
}
