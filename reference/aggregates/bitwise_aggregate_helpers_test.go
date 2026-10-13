package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func bitwiseAggregateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE bw(k TEXT,s SMALLINT,i INTEGER,b BIGINT,f BIT(5),v VARBIT(9))", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO bw VALUES ('fold',12,12,12,B'10101',B'10101'),('fold',10,10,10,B'11000',B'11000'),('fold',3,3,3,B'00111',B'00111'),('fold',NULL,NULL,NULL,NULL,NULL),('single',7,7,7,B'00101',B'00101'),('null',NULL,NULL,NULL,NULL,NULL)", 6))
}

func bitwiseAggregateTriple(pg bool, q, typ string, row []any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"bit_and", "bit_or", "bit_xor"}, []string{typ, typ, typ}, [][]any{row}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		bitwiseAggregateMetadata(t, rows, pg, s.Columns, s.Types)
	}
	return s
}

func bitwiseAggregateMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.ScalarAssertDescription(t, cols, names, types)
	fixture.ScalarAssertMetadata(t, pg, cols)
	for _, c := range cols {
		if c.DatabaseTypeName() == "BIT" {
			if c.ScanType() != reflect.TypeFor[string]() {
				t.Fatalf("%s ScanType=%v want string", c.Name(), c.ScanType())
			}
			if n, ok := c.Length(); !ok || n != -1 {
				t.Fatalf("%s aggregate Length=%d/%v want -1/true", c.Name(), n, ok)
			}
		} else if n, ok := c.Length(); ok {
			t.Fatalf("%s unexpected Length=%d", c.Name(), n)
		}
		if precision, scale, ok := c.DecimalSize(); ok {
			t.Fatalf("%s unexpected DecimalSize=%d/%d", c.Name(), precision, scale)
		}
		if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("%s Nullable=%v/%v want %v/%v", c.Name(), nullable, known, !pg, !pg)
		}
	}
}

func bitwiseAggregateRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func bitwiseAggregateWidthDiagnostic(name string) fixture.Diagnostic {
	operation := map[string]string{"bit_and": "AND", "bit_or": "OR", "bit_xor": "XOR"}[name]
	return fixture.ControlsReferenceDiagnostic("22026", "cannot "+operation+" bit strings of different sizes")
}
