package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbExistsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func jsonbExistsSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
}

func jsonbExistsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s = fixture.Prepared(s)
	if s.Diagnostic != nil {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func jsonbExistsMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != "BOOL" {
		t.Fatal(cols)
	}
	ct := cols[0]
	if ct.ScanType() != reflect.TypeFor[bool]() {
		t.Fatalf("ScanType=%v", ct.ScanType())
	}
	if n, ok := ct.Length(); ok || n != 0 {
		t.Fatalf("Length=%d,%v", n, ok)
	}
	if p, s, ok := ct.DecimalSize(); ok || p != 0 || s != 0 {
		t.Fatalf("DecimalSize=%d,%d,%v", p, s, ok)
	}
	// Direct computed projections have unknown nullability in the native
	// adapter, as in pq; nullable catalog columns use a separate contract.
	if n, ok := ct.Nullable(); n || ok {
		t.Fatalf("Nullable=%v,%v want=false,false", n, ok)
	}
}

func jsonbExistsSignature(q, signature string, position int) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+signature)
	d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	d.Position = fixture.Position(position)
	s := fixture.ErrorDiagnostic(q, d)
	s.SelectRows = true
	return s
}
