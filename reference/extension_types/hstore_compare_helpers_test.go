package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

var hstoreComparisonColumns = []string{"eq", "ne", "lt", "le", "gt", "ge", "cmp"}
var hstoreComparisonTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4"}

func hstoreComparisonProjection(left, right string, functions bool) string {
	var columns []string
	for i, op := range []string{"=", "<>", "#<#", "#<=#", "#>#", "#>=#"} {
		expression := left + " " + op + " " + right
		if functions {
			expression = "hstore_" + []string{"eq", "ne", "lt", "le", "gt", "ge"}[i] + "(" + left + "," + right + ")"
		}
		columns = append(columns, expression+" AS "+hstoreComparisonColumns[i])
	}
	return strings.Join(columns, ",") + ",hstore_cmp(" + left + "," + right + ") AS cmp"
}

func hstoreComparisonRow(sign int) []any {
	return []any{sign == 0, sign != 0, sign < 0, sign <= 0, sign > 0, sign >= 0, int64(sign)}
}

func hstoreComparisonStep(query string, row []any, args ...any) fixture.Step {
	return fixture.Select(query, hstoreComparisonColumns, hstoreComparisonTypes, [][]any{row}, args...)
}

func hstoreComparisonIDs(query string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select(query, []string{"id"}, []string{"INT4"}, rows)
}

const hstoreComparisonData = `INSERT INTO hs_cmp VALUES (1,'a=>1,b=>2'),(2,'b=>2,a=>1'),(3,NULL),(4,''),(5,'a=>NULL'),(6,'a=>""'),(7,'a=>b'),(8,NULL)`

func hstoreComparisonTable(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_cmp(id integer NOT NULL,v hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec(hstoreComparisonData, 8))
}

func hstoreComparisonUniqueDiagnostic(table, constraint string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "`+constraint+`"`)
	want.Detail = `Key (v)=("a"=>"1", "b"=>"2") already exists.`
	want.Schema, want.Table, want.Constraint = "public", table, constraint
	return want
}

func hstoreComparisonUnsupported(op string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: hstore "+op+" hstore")
	want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	want.Position = "19"
	return want
}
