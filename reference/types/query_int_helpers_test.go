package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func queryIntScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func queryIntLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func queryIntInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION intarray", 0))
}

// Values are independently specified. lib/pq's unknown extension OID uses raw
// bytes and an empty type name; native uses its actual QUERY_INT descriptor.
func queryIntSelect(b fixture.Backend, q string, columns []string, values [][]any, args ...any) fixture.Step {
	types := make([]string, len(columns))
	for i, name := range columns {
		if name == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "QUERY_INT"
		}
	}
	s := fixture.Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if columns[column] == "id" {
			return false
		}
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("query_int raw PG cell %d/%d = %T, want []byte", row, column, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("query_int raw native cell %d/%d = %T, want string", row, column, value)
		}
		return true
	}
	return s
}

// Every omitted logical field is expected empty, not ignored. PostgreSQL 18.0
// observations establish these full records before native implementation.
func queryIntError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared, selectRows, prepareFailure bool, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
	if prepared && prepareFailure {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func queryIntParameterDiagnostic(code, message, input string, parameter int) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	text := "..."
	if input == "" {
		text = ""
	}
	want.Context = fmt.Sprintf("unnamed portal parameter $%d = '%s'", parameter, text)
	return want
}

func queryIntMetadata(t *testing.T, pg bool, rows *sql.Rows, names []string, nullable []bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	queryIntColumnMetadata(t, pg, columns, names, nullable)
	return columns
}

func queryIntColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("metadata count %d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		if c.Name() != names[i] {
			t.Fatalf("metadata name %q want %q", c.Name(), names[i])
		}
		if names[i] == "id" {
			continue
		}
		wantName, wantScan := "QUERY_INT", reflect.TypeFor[string]()
		if pg {
			wantName, wantScan = "", reflect.TypeFor[any]()
		}
		if c.DatabaseTypeName() != wantName || c.ScanType() != wantScan {
			t.Fatalf("query_int metadata name=%q type=%q scan=%v want %q/%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), wantName, wantScan)
		}
		if n, known := c.Nullable(); pg || nullable == nil {
			if n || known {
				t.Fatalf("nullable %v/%v want false/false", n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatalf("nullable %v/%v want %v/true", n, known, nullable[i])
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatalf("length %d/%v want 0/false", n, known)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("decimal %d/%d/%v want 0/0/false", precision, scale, known)
		}
	}
}
