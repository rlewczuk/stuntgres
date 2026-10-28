package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func bitOperatorsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func bitOperatorsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func bitOperatorsOne(q, typ string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"r"}, []string{typ}, [][]any{{value}}, args...)
}

func bitOperatorsMetadata(t *testing.T, cols []*sql.ColumnType, names, types []string, knownNullable ...bool) {
	t.Helper()
	if len(cols) != len(names) {
		t.Fatalf("metadata count %d want %d", len(cols), len(names))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		bits := types[i] == "BIT" || types[i] == "VARBIT"
		if types[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan {
			t.Errorf("column %d: %s/%s/%v want %s/%s/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), names[i], types[i], scan)
		}
		width, known := col.Length()
		wantWidth := int64(0)
		if bits {
			wantWidth = -1
		}
		if width != wantWidth || known != bits {
			t.Errorf("Length %s: %d/%t want %d/%t", col.Name(), width, known, wantWidth, bits)
		}
		wantNullable := len(knownNullable) > 0 && knownNullable[0]
		if n, ok := col.Nullable(); n != wantNullable || ok != wantNullable {
			t.Errorf("Nullable %s: %t/%t want %t/%t", col.Name(), n, ok, wantNullable, wantNullable)
		}
		if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
			t.Errorf("DecimalSize %s: %d/%d/%t", col.Name(), p, s, ok)
		}
	}
}

func bitOperatorsError(q string, d fixture.Diagnostic, prepared, analysis, query bool, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
	if prepared && analysis {
		s.ErrorStage = "prepare"
	}
	return s
}

const bitOperatorsBinaryHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
const bitOperatorsUnaryHint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
const bitOperatorsAmbiguousHint = "Could not choose a best candidate operator. You might need to add explicit type casts."
