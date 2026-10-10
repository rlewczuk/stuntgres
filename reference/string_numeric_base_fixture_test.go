package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func stringNumericBaseScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func stringNumericBaseBoth(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			if step.diagnostic != nil {
				step.selectRows, step.errorStage = true, "execute"
			}
			if prepared {
				step = referencePrepared(step)
				if step.diagnostic != nil {
					step.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, c, b.recorder, step)
		})
	}
}

func stringNumericBaseText(q string, names []string, rows [][]any, args ...any) referenceStep {
	types := make([]string, len(names))
	for i := range types {
		types[i] = "TEXT"
	}
	s := referenceSelect(q, names, types, rows, args...)
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
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

func stringNumericBaseDiagnostic(code, message, hint, position string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: position}
}
