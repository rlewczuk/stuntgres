package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func cubeLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func cubeInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION cube", 0))
}

func cubeError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared, selectRows, prepareFailure bool, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
	if prepared && prepareFailure {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func cubeParameterDiagnostic(want fixture.Diagnostic, input string, parameter int) fixture.Diagnostic {
	text := "..."
	if input == "" {
		text = ""
	}
	want.Context = fmt.Sprintf("unnamed portal parameter $%d = '%s'", parameter, text)
	return want
}

func cubeMetadata(t *testing.T, pg bool, rows *sql.Rows, names []string, nullable []bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	cubeColumnMetadata(t, pg, columns, names, nullable)
	return columns
}

func cubeColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
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
		wantName, wantScan := "CUBE", reflect.TypeFor[string]()
		if pg {
			wantName, wantScan = "", reflect.TypeFor[any]()
		}
		if c.DatabaseTypeName() != wantName || c.ScanType() != wantScan {
			t.Fatalf("cube metadata type=%q scan=%v want %q/%v", c.DatabaseTypeName(), c.ScanType(), wantName, wantScan)
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
