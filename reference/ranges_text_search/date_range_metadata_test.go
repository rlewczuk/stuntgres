package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDateRangeMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,a daterange NOT NULL,b pg_catalog.daterange)", 0))
		run(fixture.Exec("CREATE TABLE n(v daterange)", 0))
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(b, "SELECT a AS required,b AS optional FROM r", []string{"required", "optional"}, []string{"DATERANGE", "DATERANGE"}, nil, []bool{false, true})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("INSERT INTO n VALUES (NULL)", 1))
		run(fixture.Exec("INSERT INTO r VALUES (1,'empty','[,]'),(2,'[2000-01-01,infinity]',NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(b, "SELECT v FROM n", []string{"v"}, []string{"DATERANGE"}, [][]any{{nil}}, []bool{true}),
				rangeSelect(b, "SELECT a,b FROM r ORDER BY k", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, [][]any{{"empty", "(,)"}, {"[2000-01-01,infinity]", nil}}, []bool{false, true}),
				rangeSelect(b, "SELECT a,b FROM r WHERE k=0", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, nil, []bool{false, true}),
			} { s.Prepared = prepared; run(s) }
		}
		check := rangeSelect(b, "SELECT a,b FROM r ORDER BY k", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, [][]any{{"empty", "(,)"}, {"[2000-01-01,infinity]", nil}}, []bool{false, true})
		rows, err := b.DB.QueryContext(p.Ctx, check.Query)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		fixture.RangeConsumeHeld(t, rows, check)
		for i, typ := range metadata {
			wantScan := reflect.TypeFor[string]()
			if b.PG { wantScan = reflect.TypeFor[any]() }
			if typ.Name() != check.Columns[i] || typ.DatabaseTypeName() != "DATERANGE" || typ.ScanType() != wantScan { t.Fatal("retained metadata changed", typ) }
			if n, known := typ.Nullable(); b.PG && (n || known) || !b.PG && (!known || n != (i == 1)) { t.Fatal("retained nullable", n, known) }
			if n, known := typ.Length(); n != 0 || known { t.Fatal("retained length", n, known) }
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known { t.Fatal("retained precision", precision, scale, known) }
		}
	}
}
