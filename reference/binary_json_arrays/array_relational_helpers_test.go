package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayRelationalScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func arrayRelationalBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	arrayInspectBoth(t, p, b, c, s)
}

// Binding errors fail at Prepare; runtime count errors fail at execution even
// when the call's operands are constants. Full records include absent fields.
func arrayRelationalError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, runtime bool, args ...any) {

	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared && !runtime {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
func arrayRelationalArrayMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	s := fixture.ArrayReferenceSelect("", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, nil, pg)
	s.CheckMetadata(t, rows)
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != "v" || columns[0].DatabaseTypeName() != "_INT4" {
		t.Fatalf("wrong array descriptor: %v", columns)
	}
	if n, known := columns[0].Nullable(); n || known {
		t.Fatalf("array nullability %t/%t", n, known)
	}
	if p, s, known := columns[0].DecimalSize(); p != 0 || s != 0 || known {
		t.Fatalf("array decimal %d,%d/%t", p, s, known)
	}
}

func arrayRelationalRows(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared bool, args []any, check func(*sql.Rows)) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		st, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer st.Close()
		rows, err = st.QueryContext(p.Ctx, args...)
	} else {
		rows, err = c.QueryContext(p.Ctx, q, args...)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	check(rows)
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
