package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations are independently specified from REL_18_0 and pinned oracle
// observations. No expectation is learned from native output.
func aclitemScenario(t *testing.T, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, role := range roles {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE "+aclitemSQLName(role), 0))
			}
			body(t, p, b, c)
		})
	}
}

func aclitemSQLName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func aclitemArray(elements ...string) string {
	parts := make([]string, len(elements))
	for i, element := range elements {
		parts[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(element) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func aclitemSelect(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"a"}, []string{"ACLITEM"}, rows, args...)
}

func aclitemDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func aclitemMetadata(t *testing.T, pg bool, rows *sql.Rows, names, types []string, nullable []bool, computed bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.ScalarAssertDescription(t, columns, names, types)
	for i, column := range columns {
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if column.ScanType() != want {
			t.Fatalf("%s ScanType=%v want %v", column.Name(), column.ScanType(), want)
		}
		if length, known := column.Length(); length != 0 || known {
			t.Fatalf("%s Length=%d/%v", column.Name(), length, known)
		}
		if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("%s DecimalSize=%d/%d/%v", column.Name(), precision, scale, known)
		}
		got, known := column.Nullable()
		if pg || computed {
			if got || known {
				t.Fatalf("%s Nullable=%v/%v want unknown", column.Name(), got, known)
			}
		} else if !known || got != nullable[i] {
			t.Fatalf("%s Nullable=%v/%v want %v/true", column.Name(), got, known, nullable[i])
		}
	}
	return columns
}
