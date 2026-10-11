package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericRangeBinds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("CREATE TABLE r(k integer NOT NULL,a numrange,b numrange)", 0)))
		stmt, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO r VALUES ($2,$1,$1)")
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		data := []byte("[001.2300,2e1]")
		want := [][]any{}
		for i, tc := range []struct{ value, output any }{
			{"[1.25,4.7500]", "[1.25,4.7500]"}, {"(4,5)", "(4,5)"},
			{nil, nil}, {[]byte(nil), nil}, {data, "[1.2300,20]"},
		} {
			result, err := stmt.ExecContext(p.Ctx, tc.value, i+1)
			if err != nil { t.Fatal(err) }
			if n, err := result.RowsAffected(); err != nil || n != 1 { t.Fatal("affected", n, err) }
			want = append(want, []any{int64(i+1), tc.output, tc.output})
		}
		for i := range data { data[i] = 'x' }
		for _, tc := range []integerRangeFailure{
			{"", "22P02", `malformed range literal: ""`, "Missing left parenthesis or bracket."},
			{"[oops,4)", "22P02", `invalid input syntax for type numeric: "oops"`, ""},
			{"[0,1e-16384)", "22003", "value overflows numeric format", ""},
			{"[4.01,4.00)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		} {
			value := any(tc.input)
			if tc.input == "" { value = []byte{} }
			_, err := stmt.ExecContext(p.Ctx, value, 90)
			diagnostic := numericRangeDiagnostic(tc)
			diagnostic.Context = `unnamed portal parameter $1 = '...'`
			if tc.input == "" { diagnostic.Context = `unnamed portal parameter $1 = ''` }
			fixture.AssertDiagnostic(t, err, &diagnostic)
		}
		result, err := stmt.ExecContext(p.Ctx, "[1.0,1.00]", 6)
		if err != nil { t.Fatal(err) }
		if n, err := result.RowsAffected(); err != nil || n != 1 { t.Fatal(n, err) }
		want = append(want, []any{int64(6), "[1.0,1.00]", "[1.0,1.00]"})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES ($1,$2,$2)", 1, 7, []byte("[-inf,+INF]")))
		want = append(want, []any{int64(7), "[-Infinity,Infinity]", "[-Infinity,Infinity]"})
		check := rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "NUMRANGE", "NUMRANGE"}, want, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(check))
	}
}
