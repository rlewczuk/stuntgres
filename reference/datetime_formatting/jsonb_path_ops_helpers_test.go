package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

var jsonbPathOps = []string{"@?", "@@"}

func jsonbPathOpsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonbPathOpsSQL(op string) string {
	return `SELECT $1::jsonb ` + op + ` $2::jsonpath AS changed`
}

func jsonbPathOpsOne(q string, value any, args ...any) fixture.Step {
	return jsonpathQueryOne(q, "BOOL", value, args...)
}

func jsonbPathOpsMetadata(s fixture.Step) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.Columns()
		if err != nil || len(columns) != 1 || columns[0] != "changed" {
			t.Fatalf("columns=%v err=%v", columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 {
			t.Fatalf("types=%v err=%v", types, err)
		}
		if types[0].Name() != "changed" || types[0].DatabaseTypeName() != "BOOL" {
			t.Fatal(types[0])
		}
		fixture.ScalarAssertMetadata(t, false, types, 0)
		if n, known := types[0].Length(); n != 0 || known {
			t.Fatalf("BOOL length=(%d,%v)", n, known)
		}
		if p, s, known := types[0].DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("BOOL precision=(%d,%d,%v)", p, s, known)
		}
	}
	return s
}
