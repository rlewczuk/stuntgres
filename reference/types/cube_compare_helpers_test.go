package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeComparisonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

var cubeComparisonColumns = []string{"eq", "ne", "lt", "le", "gt", "ge", "cmp"}
var cubeComparisonTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4"}

func cubeComparisonProjection(left, right string, functions bool) string {
	var columns []string
	operators := []string{"=", "<>", "<", "<=", ">", ">="}
	functionsNames := []string{"cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge"}
	for i, op := range operators {
		expression := left + " " + op + " " + right
		if functions {
			expression = "public." + functionsNames[i] + "(" + left + "," + right + ")"
		}
		columns = append(columns, expression+" AS "+cubeComparisonColumns[i])
	}
	columns = append(columns, "public.cube_cmp("+left+","+right+") AS cmp")
	return strings.Join(columns, ",")
}

func cubeComparisonRow(sign int) []any {
	return []any{sign == 0, sign != 0, sign < 0, sign <= 0, sign > 0, sign >= 0, int64(sign)}
}

func cubeComparisonStep(query string, row []any, args ...any) fixture.Step {
	return fixture.Select(query, cubeComparisonColumns, cubeComparisonTypes, [][]any{row}, args...)
}

func cubeComparisonIDs(query string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select(query, []string{"id"}, []string{"INT4"}, rows)
}

const cubeComparisonData = "INSERT INTO cube_compare_data VALUES (1,'(2)'),(2,'(1)'),(3,'1.0'),(4,'(-1)'),(5,NULL),(6,'(-0)'),(7,'(0)')"

func cubeComparisonTable(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_compare_data(id integer NOT NULL,v cube)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(cubeComparisonData, 7))
}

func cubeComparisonUniqueDiagnostic(table, constraint, value string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "`+constraint+`"`)
	want.Detail = "Key (v)=(" + value + ") already exists."
	want.Schema, want.Table, want.Constraint = "public", table, constraint
	return want
}
