package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations in this family come from REL_18_6 int.c, oid.c, numutils.c,
// arrayfuncs.c, nbtcompare.c and pg_type.dat, and lib/pq v1.12.3's text codec.
func catalogVectorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func catalogVectorQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func catalogVectorRows(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, s fixture.Step) {
	t.Helper()
	// pq exports OID decimal bytes; native exports int64. Keep raw validation.
	if b.PG {
		copied := make([][]any, len(s.Rows))
		for r, row := range s.Rows {
			copied[r] = append([]any(nil), row...)
			for i, typ := range s.Types {
				if typ == "OID" && row[i] != nil { copied[r][i] = strconv.FormatInt(row[i].(int64), 10) }
			}
		}
		s.Rows = copied
	}
	previous := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		if previous != nil { previous(t, rows) }
		columns, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		for i, typ := range s.Types {
			if typ != "INT2VECTOR" && typ != "OIDVECTOR" { continue }
			want := reflect.TypeFor[string]()
			if b.PG { want = reflect.TypeFor[any]() }
			if columns[i].ScanType() != want { t.Fatalf("%s ScanType=%v want %v", typ, columns[i].ScanType(), want) }
			if n, ok := columns[i].Length(); n != 0 || ok { t.Fatalf("vector length=%d/%t", n, ok) }
			if a, z, ok := columns[i].DecimalSize(); a != 0 || z != 0 || ok { t.Fatalf("vector precision=%d/%d/%t", a, z, ok) }
		}
	}
	s.CheckRaw = func(t *testing.T, i, row int, v any) bool {
		typ := s.Types[i]
		if typ != "INT2VECTOR" && typ != "OIDVECTOR" { return false }
		if v != nil {
			if b.PG {
				data, ok := v.([]byte)
				if !ok || data == nil { t.Fatalf("pq %s raw=%T %#v", typ, v, v) }
			} else if _, ok := v.(string); !ok { t.Fatalf("native %s raw=%T", typ, v) }
		}
		if !reflect.DeepEqual(fixture.Normalize(v), s.Rows[row][i]) { t.Fatalf("raw %s=%#v want %#v", typ, v, s.Rows[row][i]) }
		return true
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
}

func catalogVectorDiagnostic(code, typ, suffix string) fixture.Diagnostic {
	message := "invalid input syntax for type " + typ + ": \"" + suffix + "\""
	if code == "22003" { message = "value \"" + suffix + "\" is out of range for type " + typ }
	return fixture.ControlsReferenceDiagnostic(code, message)
}
