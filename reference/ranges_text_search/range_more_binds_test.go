package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreBinds(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, f := range moreFamilies {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_bind(a "+f.rangeName+",m "+f.multiName+")", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_bind VALUES (NULL,NULL)", 1))
			for _, expansion := range []bool{false, true} {
				q := "SELECT multirange(CASE WHEN true THEN $1 ELSE a END) AS v FROM more_bind"
				typ := f.multiType
				finite, empty, universal := f.finite, "empty", "(,)"
				finiteRows := [][]any{{"{" + f.canonical + "}"}}
				emptyRows, nullRows, universalRows := [][]any{{"{}"}}, [][]any{{nil}}, [][]any{{"{(,)}"}}
				other, otherRows := "[20,21)", [][]any{{"{[20,21)}"}}
				if expansion {
					q = "SELECT unnest(CASE WHEN true THEN $1 ELSE m END) AS v FROM more_bind"
					typ = f.rangeType
					finite, empty, universal = f.members, "{}", "{(,)}"
					finiteRows = [][]any{{f.first}, {f.second}}
					emptyRows, nullRows, universalRows = [][]any{}, [][]any{}, [][]any{{"(,)"}}
					other, otherRows = "{[20,21)}", [][]any{{"[20,21)"}}
				}
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					for _, tc := range []struct {
						input any
						rows  [][]any
					}{{finite, finiteRows}, {[]byte(finite), finiteRows}, {empty, emptyRows}, {nil, nullRows}, {[]byte(nil), nullRows}, {universal, universalRows}, {other, otherRows}, {finite, finiteRows}} {
						s := rangeSelect(b, q, []string{"v"}, []string{typ}, tc.rows, nil)
						s.Args = []any{tc.input}
						moreBoth(t, p, b, s)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					message, detail := `malformed range literal: "bad"`, "Missing left parenthesis or bracket."
					if expansion {
						message, detail = `malformed multirange literal: "bad"`, "Missing left brace."
					}
					failures := []struct{ input, code, message, detail string }{
						{"bad", "22P02", message, detail},
						{"[4,1)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
					}
					if f.rangeName == "int4range" {
						failures = append(failures, struct{ input, code, message, detail string }{"[1,2147483647]", "22003", "integer out of range", ""})
					}
					if f.rangeName == "int8range" {
						failures = append(failures, struct{ input, code, message, detail string }{"[1,9223372036854775807]", "22003", "bigint out of range", ""})
					}
					for _, tc := range failures {
						input := tc.input
						if expansion && input != "bad" { input = "{" + input + "}" }
						failure := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, tc.code, tc.message, "", "", tc.detail, "unnamed portal parameter $1 = '...'"), input)
						failure.SelectRows, failure.ErrorStage = true, "execute"
						moreBoth(t, p, b, failure)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
						recovered := rangeSelect(b, q, []string{"v"}, []string{typ}, finiteRows, nil)
						recovered.Args = []any{finite}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, recovered)
					}
					owned := []byte(finite)
					rows, err := stmt.QueryContext(p.Ctx, owned)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					for i := range owned {
						owned[i] = 'x'
					}
					fixture.RangeConsumeHeld(t, rows, rangeSelect(b, q, []string{"v"}, []string{typ}, finiteRows, nil))
				}()
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE more_bind", 0))
		}
	})
}
