package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently specified from the manual and REL_18_0.
// Root's pinned PostgreSQL oracle validated the shared SQL definitions;
// native-only control phases remain separate public API contracts.
func datetimeJustifyScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
			body(t, p, b, c)
		})
	}
}

func datetimeJustifyMetadata(t *testing.T, pg bool, rows *sql.Rows) {
	t.Helper()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meta {
		if m.DatabaseTypeName() != "INTERVAL" {
			t.Fatalf("type = %q; want INTERVAL", m.DatabaseTypeName())
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if m.ScanType() != want {
			t.Fatalf("ScanType = %v; want %v", m.ScanType(), want)
		}
		if _, ok := m.Length(); ok {
			t.Fatal("interval has length metadata")
		}
		if _, _, ok := m.DecimalSize(); ok {
			t.Fatal("interval has decimal metadata")
		}
	}
}

func datetimeJustifyBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			if s.SelectRows && s.State == "" {
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.PG, rows) }
			}
			if s.State != "" {
				s.ErrorStage = "execute"
				if prepared && s.Diagnostic.Position != "" {
					s.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		})
	}
}

func datetimeJustifyValue(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, expr string, want any) {
	t.Helper()
	datetimeJustifyBoth(t, p, b, x, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"INTERVAL"}, [][]any{{want}}))
}

func datetimeJustifyFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q, code, message, hint string, position int) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint, d.Position = hint, fixture.Position(position)
	s := fixture.ErrorDiagnostic(q, d)
	s.SelectRows = true
	datetimeJustifyBoth(t, p, b, x, s)
}
