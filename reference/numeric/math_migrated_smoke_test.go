package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeMathSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			for _, tc := range []struct {
				query, typ string
				want       any
			}{
				{"SELECT round(2.5)", "NUMERIC", "3"},
				{"SELECT round(-2.5)", "NUMERIC", "-3"},
				{"SELECT round(-1.25,1)", "NUMERIC", "-1.3"},
				{"SELECT round(1500,-3)", "NUMERIC", "2000"},
				{"SELECT round(1.23,4)", "NUMERIC", "1.2300"},
				{"SELECT round(4)", "FLOAT8", float64(4)},
				{"SELECT round('2.5')", "FLOAT8", float64(2)},
				{"SELECT trunc(-1.25,1)", "NUMERIC", "-1.2"},
				{"SELECT trunc(-1500,-3)", "NUMERIC", "-1000"},
				{"SELECT scale(8.4100)", "INT4", int64(4)},
				{"SELECT min_scale(8.4100)", "INT4", int64(2)},
				{"SELECT trim_scale(8.4100)", "NUMERIC", "8.41"},
				{"SELECT trim_scale(0.0000)", "NUMERIC", "0"},
				{"SELECT sign(-13.000)", "NUMERIC", "-1"},
				{"SELECT sign('NaN')", "FLOAT8", float64(0)},
				{"SELECT mod(-9,4)", "INT4", int64(-1)},
				{"SELECT mod(-9.00,4.0)", "NUMERIC", "-1.00"},
				{"SELECT mod(9007199254740993.25,2.0)", "NUMERIC", "1.25"},
				{"SELECT mod(1.2300,'Infinity')", "NUMERIC", "1.2300"},
				{"SELECT mod('NaN',0.0)", "NUMERIC", "NaN"},
				{"SELECT round(NULL,2)", "NUMERIC", nil},
				{"SELECT scale(NULL)", "INT4", nil},
				{"SELECT min_scale('NaN')", "INT4", nil},
				{"SELECT mod(1,NULL)", "INT4", nil},
			} {
				name := strings.SplitN(strings.TrimPrefix(tc.query, "SELECT "), "(", 2)[0]
				step := fixture.Select(tc.query, []string{name}, []string{tc.typ}, [][]any{{tc.want}})
				run(step)
				run(fixture.Prepared(step))
			}
			run(fixture.Exec("CREATE TABLE math_float(d DOUBLE PRECISION)", 0))
			run(fixture.Exec("INSERT INTO math_float VALUES('-0.5')", 1))
			for _, q := range []string{"SELECT round('-0.5')", "SELECT trunc(d) FROM math_float"} {
				name := strings.SplitN(strings.TrimPrefix(q, "SELECT "), "(", 2)[0]
				step := fixture.Select(q, []string{name}, []string{"FLOAT8"}, [][]any{{math.Copysign(0, -1)}})
				step.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var got float64
					if err := rows.Scan(&got); err != nil || got != 0 || !math.Signbit(got) {
						t.Fatal(q, got, err)
					}
				}
				run(step)
			}
			const query = "SELECT round($1,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, tc := range []struct{ input, want any }{{"1.25", "1.3"}, {"-1.25", "-1.3"}, {nil, nil}, {"1.24", "1.2"}} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"round"}, []string{"NUMERIC"}, [][]any{{tc.want}}, tc.input, 1))
			}
			failure := fixture.ImplicitError("SELECT mod(1,0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			failure.ErrorStage = "execute"
			run(fixture.Prepared(failure))
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE source(n NUMERIC)", 0), fixture.Exec("INSERT INTO source VALUES(1.23)", 1),
				fixture.Exec("CREATE TABLE stored(padded NUMERIC,trimmed NUMERIC)", 0), fixture.Exec("INSERT INTO stored SELECT round(n,4),trim_scale(n) FROM source", 1),
			} {
				run(step)
			}
			stored := fixture.Select("SELECT padded,trimmed FROM stored", []string{"padded", "trimmed"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2300", "1.23"}})
			run(stored)
			if backend.PG {
				return
			} // Native public control continuation.
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
			run(stored)
		})
	}
}
