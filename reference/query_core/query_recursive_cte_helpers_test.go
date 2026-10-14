package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func recursiveCTEReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

const recursiveCTEFinite = "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r"
const recursiveCTELate = "WITH RECURSIVE r(n,v) AS (SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM r WHERE n<4) "

func recursiveCTEInts(values ...int64) [][]any {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return rows
}

// Reuse the installed synthetic set-output raw metadata convention. This runs
// before Next, including empty anchors, and does not infer types from values.
func recursiveCTEMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(names) {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			wantScan := reflect.TypeFor[int64]()
			if types[i] == "INT4" && b.PG {
				wantScan = reflect.TypeFor[int32]()
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != wantScan {
				t.Fatalf("recursive descriptor %d: %v", i, c)
			}
			if value, known := c.Nullable(); value || known {
				t.Fatalf("nullable %t/%t", value, known)
			}
			if value, known := c.Length(); value != 0 || known {
				t.Fatalf("length %d/%t", value, known)
			}
			if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("decimal %d,%d/%t", precision, scale, known)
			}
		}
	}
}

func recursiveCTERoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	subqueryBoth(run, s)
	plain := s
	plain.NonContext = true
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, plain)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(plain))
	held := fixture.ViewHold(t, p, b, s.Query)
	for range 2 {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	if s.SelectRows && s.State == "" {
		subqueryBoth(run, fixture.Exec(s.Query, int64(len(s.Rows)), s.Args...))
	}
}
