package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeComparisonScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

var ltreeComparisonColumns = []string{"eq", "ne", "lt", "le", "gt", "ge"}
var ltreeComparisonTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}

func ltreeComparisonProjection(left, right string) string {
	columns := make([]string, 6)
	for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		columns[i] = left + " " + op + " " + right + " AS " + ltreeComparisonColumns[i]
	}
	return strings.Join(columns, ",")
}

func ltreeComparisonRow(sign int) []any {
	return []any{sign == 0, sign != 0, sign < 0, sign <= 0, sign > 0, sign >= 0}
}

func ltreeComparisonStep(q string, row []any, args ...any) fixture.Step {
	s := fixture.Select(q, ltreeComparisonColumns, ltreeComparisonTypes, [][]any{row}, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value != nil { if _,ok := value.(bool); !ok { t.Fatalf("comparison raw value %T",value) } }
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		ltreeArrayMetadata(t,fixture.Backend{},rows,ltreeComparisonTypes,nil)
	}
	return s
}

func ltreeComparisonRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func ltreeComparisonIDs(q string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids { rows[i] = []any{id} }
	return fixture.Select(q, []string{"id"}, []string{"INT4"}, rows)
}

func ltreeComparisonTable(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE TABLE lt_cmp(id integer NOT NULL,v ltree)", 0))
	ltreeComparisonRun(t, p, b, c, fixture.Exec("INSERT INTO lt_cmp VALUES (1,'a-b'),(2,'a.b'),(3,'a')", 3))
	q := "INSERT INTO lt_cmp VALUES ($1,$2::ltree)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, args := range [][]any{{4, ""}, {5, nil}, {6, []byte("a.b")}} {
		ltreeComparisonRun(t, p, b, held, fixture.Exec(q, 1, args...))
	}
}

func ltreeComparisonUniqueDiagnostic() fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "lt_cmp_unique_key"`)
	want.Detail = "Key (v)=(a.b) already exists."
	want.Schema, want.Table, want.Constraint = "public", "lt_cmp_unique", "lt_cmp_unique_key"
	return want
}
