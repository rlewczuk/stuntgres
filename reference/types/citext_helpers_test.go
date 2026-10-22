package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func citextScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func citextInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION citext", 0))
}

func citextSelect(b fixture.Backend, q string, names []string, values [][]any, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i, n := range names {
		if n == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "CITEXT"
		}
	}
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if names[col] == "id" {
			return false
		}
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("citext PG raw cell %d/%d: %T, want []byte", row, col, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("citext native raw cell %d/%d: %T, want string", row, col, value)
		}
		return true
	}
	return s
}

func citextColumnMetadata(t *testing.T, pg bool, cols []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(cols) != len(names) {
		t.Fatalf("metadata count %d want %d", len(cols), len(names))
	}
	for i, col := range cols {
		if col.Name() != names[i] {
			t.Fatalf("column %q want %q", col.Name(), names[i])
		}
		if names[i] == "id" {
			continue
		}
		name, scan := "CITEXT", reflect.TypeFor[string]()
		if pg {
			name, scan = "", reflect.TypeFor[any]()
		}
		if col.DatabaseTypeName() != name || col.ScanType() != scan {
			t.Fatalf("citext metadata %q/%v want %q/%v", col.DatabaseTypeName(), col.ScanType(), name, scan)
		}
		n, known := col.Nullable()
		if pg || nullable == nil {
			if n || known {
				t.Fatalf("nullable %v/%v want false/false", n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatalf("nullable %v/%v want %v/true", n, known, nullable[i])
		}
		if n, known := col.Length(); n != 0 || known {
			t.Fatalf("length %d/%v", n, known)
		}
		if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("decimal %d/%d/%v", p, s, known)
		}
	}
}

func citextMetadata(t *testing.T, pg bool, rows *sql.Rows, names []string, nullable []bool) []*sql.ColumnType {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	citextColumnMetadata(t, pg, cols, names, nullable)
	return cols
}

// Every omitted logical diagnostic field is independently expected empty.
// These records were independently asserted against pinned PostgreSQL 18.6.
func citextDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func citextPosition(q, token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}

func citextError(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, q string, prepared, query, prepareFailure bool, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
	if prepared && prepareFailure {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
}

const citextLiteralInsert = "INSERT INTO citext_values VALUES (1,'MiXeD'),(2,''),(3,'  Straße İ Σς é🙂  '),(4,'é'),(5,E'O''Reilly\\\\path\\nNext'),(6,NULL)"

func citextLiteralRows() [][]any {
	return [][]any{{int64(1), "MiXeD"}, {int64(2), ""}, {int64(3), "  Straße İ Σς é🙂  "}, {int64(4), "e\u0301"}, {int64(5), "O'Reilly\\path\nNext"}, {int64(6), nil}}
}
