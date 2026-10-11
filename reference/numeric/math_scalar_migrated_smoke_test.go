package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeMathScalarSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			for _, tc := range []struct {
				query, typ string
				want       any
			}{
				{"SELECT abs(-17.400)", "NUMERIC", "17.400"}, {"SELECT ceil(-42.8)", "NUMERIC", "-42"}, {"SELECT ceiling(-42.8)", "NUMERIC", "-42"}, {"SELECT floor(-42.8)", "NUMERIC", "-43"},
				{"SELECT div(9007199254740993,2)", "NUMERIC", "4503599627370496"}, {"SELECT exp(1." + strings.Repeat("0", 70) + ")", "NUMERIC", "2.7182818284590452353602874713526624977572470936999595749669676277240766"}, {"SELECT factorial(20)", "NUMERIC", "2432902008176640000"},
				{"SELECT abs(NULL)", "FLOAT8", nil}, {"SELECT factorial(NULL)", "NUMERIC", nil},
			} {
				name := strings.SplitN(strings.TrimPrefix(tc.query, "SELECT "), "(", 2)[0]
				step := fixture.Select(tc.query, []string{name}, []string{tc.typ}, [][]any{{tc.want}})
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if tc.typ == "NUMERIC" {
						precision, scale, known := meta[0].DecimalSize()
						if backend.PG {
							if !known || precision != 65535 || scale != 65531 {
								t.Fatal("pq derived typmod", precision, scale, known)
							}
						} else if known {
							t.Fatal("derived typmod")
						}
					}
				}
				run(step)
				run(fixture.Prepared(step))
			}
			run(fixture.Exec("CREATE TABLE widths(s SMALLINT,r REAL)", 0))
			run(fixture.Exec("INSERT INTO widths VALUES(-17,-17.5)", 1))
			widths := fixture.Select("SELECT abs(s),abs(r) FROM widths", []string{"abs", "abs"}, []string{"INT2", "FLOAT4"}, [][]any{{int64(17), float64(17.5)}})
			widths.NonContext = true
			widths.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var i int64
				var r float64
				if err := rows.Scan(&i, &r); err != nil || i != 17 || r != 17.5 {
					t.Fatal(i, r, err)
				}
			}
			run(widths)
			const query = "SELECT div($1,$2)"
			stmt, err := backend.DB.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, tc := range []struct{ a, want any }{{"9007199254740993", "4503599627370496"}, {nil, nil}, {"-9.00", "-4"}, {"9.00", "4"}} {
				step := fixture.Select(query, []string{"div"}, []string{"NUMERIC"}, [][]any{{tc.want}}, tc.a, "2")
				step.NonContext = true
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
			}
			for _, tc := range []struct{ q, code, message string }{{"SELECT div(1,0)", "22012", "division by zero"}, {"SELECT factorial(-1)", "22003", "factorial of a negative number is undefined"}} {
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				step.ErrorStage = "execute"
				step.NonContext = true
				run(fixture.Prepared(step))
			}
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE source(id INT,n NUMERIC,d NUMERIC)", 0), fixture.Exec("INSERT INTO source VALUES(1,-9.00,4),(2,9.00,0)", 2), fixture.Exec("CREATE TABLE stored(n NUMERIC)", 0),
			} {
				run(step)
			}
			for _, q := range []string{"UPDATE source SET n=div(n,d)", "INSERT INTO stored SELECT div(n,d) FROM source"} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				step.ErrorStage = "execute"
				step.NonContext = true
				run(step)
			}
			unchanged := fixture.Select("SELECT n FROM source WHERE id=1", []string{"n"}, []string{"NUMERIC"}, [][]any{{"-9.00"}})
			unchanged.NonContext = true
			run(unchanged)
			empty := fixture.Select("SELECT count(*) FROM stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
			empty.NonContext = true
			run(empty)
			run(fixture.Exec("INSERT INTO stored SELECT abs(n) FROM source", 2))
			run(fixture.Exec("CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM source", 0))
			view := fixture.Select("SELECT c,cc,f,q,e,fac FROM scalar_view", []string{"c", "cc", "f", "q", "e", "fac"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"-9", "-9", "-9", "-2", "1.0000000000000000", "120"}, {"9", "9", "9", "2", "1.0000000000000000", "120"}})
			run(view)
			stored := fixture.Select("SELECT n FROM stored", []string{"n"}, []string{"NUMERIC"}, [][]any{{"9.00"}, {"9.00"}})
			stored.NonContext = true
			run(stored)
			if backend.PG {
				return
			} // Native eager Rows and catalog controls.
			retained, err := p.Native.Query("SELECT n FROM stored")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(view)
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var n string
			if err := retained.Scan(&n); err != nil || n != "9.00" {
				t.Fatal(n, err)
			}
			meta, err := retained.ColumnTypes()
			if err != nil || meta[0].DatabaseTypeName() != "NUMERIC" {
				t.Fatal(meta, err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
				t.Fatal("roundtrip", err)
			}
		})
	}
}
