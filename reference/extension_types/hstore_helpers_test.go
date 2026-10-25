package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func hstoreLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func hstoreRun(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
}

func hstoreInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("CREATE EXTENSION hstore", 0))
}

func hstoreColumns(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns %d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		if c.Name() != names[i] {
			t.Fatalf("column %d name %q want %q", i, c.Name(), names[i])
		}
		if names[i] == "id" {
			continue
		}
		name, scan := "HSTORE", reflect.TypeFor[string]()
		if pg {
			name, scan = "", reflect.TypeFor[any]()
		}
		if c.DatabaseTypeName() != name || c.ScanType() != scan {
			t.Fatalf("%s type=%q scan=%v want %q/%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), name, scan)
		}
		n, known := c.Nullable()
		if pg || nullable == nil {
			if n || known {
				t.Fatalf("%s nullable %v/%v want false/false", c.Name(), n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatalf("%s nullable %v/%v want %v/true", c.Name(), n, known, nullable[i])
		}
		if n, ok := c.Length(); n != 0 || ok {
			t.Fatalf("length %d/%v", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("decimal %d/%d/%v", p, s, ok)
		}
	}
}

func hstoreSelect(b fixture.Backend, q string, names []string, values [][]any, nullable []bool, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i, n := range names {
		if n == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "HSTORE"
		}
	}
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if names[column] == "id" {
			return false
		}
		if v != nil {
			if b.PG {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("raw %d/%d=%T want []byte", row, column, v)
				}
			} else if _, ok := v.(string); !ok {
				t.Fatalf("raw %d/%d=%T want string", row, column, v)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		hstoreColumns(t, b.PG, columns, names, nullable)
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		dest := make([]any, len(names))
		cells := make([]sql.NullString, len(names))
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, n := range names {
			if n == "id" {
				continue
			}
			want := values[index][i]
			if cells[i].Valid != (want != nil) || (want != nil && cells[i].String != want) {
				t.Fatalf("NullString row %d column %d=%#v want %#v", index, i, cells[i], want)
			}
			if want != nil && len(names) == 1 {
				var text string
				if err := rows.Scan(&text); err != nil || text != want {
					t.Fatalf("string scan %q/%v want %v", text, err, want)
				}
			}
		}
	}
	return s
}

func hstoreParameterDiagnostic(code, message string, parameter int) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", parameter)
	return want
}

func hstoreError(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, q string, prepared, query, prepareFailure bool, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
	if prepared && prepareFailure {
		s.ErrorStage = "prepare"
	}
	hstoreRun(t, p, b, db, s)
}
