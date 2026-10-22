package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// One scenario body serves both backends.
func recursiveViewReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func recursiveViewInts(values ...int64) [][]any {
	rows := make([][]any, len(values))
	for i, n := range values {
		rows[i] = []any{n}
	}
	return rows
}

func recursiveViewRows(q, typ string, values ...int64) fixture.Step {
	s := fixture.Select(q, []string{"n"}, []string{typ}, recursiveViewInts(values...))
	s.Bag = true
	return s
}

func recursiveViewPosition(t *testing.T, q, marker string) string {
	t.Helper()
	i := strings.Index(q, marker)
	if i < 0 {
		t.Fatal("missing diagnostic marker", marker)
	}
	return fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
}

// Utility analysis happens at Execute; grammar and recursive-legality checks
// happen at Prepare. The stage is explicit because 42601 can arise in either.
func recursiveViewAnalysisFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, preparedStage string) {
	t.Helper()
	t.Run(q, func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(route, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = preparedStage
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
	})
}

func recursiveViewMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, names) {
			t.Fatal(columns, err)
		}
		ct, err := rows.ColumnTypes()
		if err != nil || len(ct) != len(types) {
			t.Fatal(ct, err)
		}
		for i, col := range ct {
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
				t.Fatal(col)
			}
			if !b.PG {
				if nullable, ok := col.Nullable(); !ok || !nullable {
					t.Fatal("view nullability", nullable, ok)
				}
			}
		}
	}
}
