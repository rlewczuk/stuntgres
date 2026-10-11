package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func stringNumericBaseScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func stringNumericBaseBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			if step.Diagnostic != nil {
				step.SelectRows, step.ErrorStage = true, "execute"
			}
			if prepared {
				step = fixture.Prepared(step)
				if step.Diagnostic != nil {
					step.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		})
	}
}

func stringNumericBaseText(q string, names []string, rows [][]any, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i := range types {
		types[i] = "TEXT"
	}
	s := fixture.Select(q, names, types, rows, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stringNumericBaseColumns(t, ct, names)
	}
	return s
}

func stringNumericBaseColumns(t *testing.T, ct []*sql.ColumnType, names []string) {
	t.Helper()
	if len(ct) != len(names) {
		t.Fatalf("metadata width %d want %d", len(ct), len(names))
	}
	for i, col := range ct {
		length, lk := col.Length()
		nullable, nk := col.Nullable()
		precision, scale, dk := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != "TEXT" || col.ScanType() != reflect.TypeFor[string]() || length != 9223372036854775807 || !lk || nullable || nk || precision != 0 || scale != 0 || dk {
			t.Fatalf("column %d: %s %s scan=%v length=%d/%v nullable=%v/%v decimal=%d,%d/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lk, nullable, nk, precision, scale, dk)
		}
	}
}

func stringNumericBaseDiagnostic(code, message, hint, position string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: position}
}
