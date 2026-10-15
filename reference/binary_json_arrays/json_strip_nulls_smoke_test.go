package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsFeatureSmoke(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_smoke(id int,v json)", 0))
		jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO js_smoke VALUES(1,$1),(2,'null'),(3,NULL)", 3, jsonStripInput)))
		for _, tc := range []struct{ option, want string }{{"", jsonStripDefault}, {",true", jsonStripArrays}} {
			s := fixture.Select("SELECT json_strip_nulls(v"+tc.option+") AS stripped FROM js_smoke ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{tc.want}, {"null"}, {nil}})
			jsonTypeBoth(t, p, b, c, jsonStripMetadata(s, b.PG))
		}
		q := "SELECT json_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ flag, want any }{{true, jsonStripArrays}, {false, jsonStripDefault}, {nil, nil}} {
				jsonTypeRun(t, p, b, held, jsonStripMetadata(jsonStripSelect(q, tc.want, jsonStripInput, tc.flag), b.PG))
			}
		}()
		bad := jsonTypeError("SELECT json_strip_nulls('oops')", "22P02", "invalid input syntax for type json", 25)
		bad.Diagnostic.Detail, bad.Diagnostic.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
		jsonTypeAnalysisError(t, p, b, c, bad)
		stable := jsonTypeError("CREATE INDEX js_smoke_index ON js_smoke ((json_strip_nulls(v)::text))", "42P17", "functions in index expression must be marked IMMUTABLE", 0)
		stable.SelectRows = false
		jsonTypeBoth(t, p, b, c, stable)
		check := jsonStripStored(t, p, b, c)
		constraint := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "js_stored" violates check constraint "js_clean"`)
		constraint.Detail = `Failing row contains (4, "bad", t).`
		constraint.Schema, constraint.Table, constraint.Constraint = "public", "js_stored", "js_clean"
		s := fixture.ErrorDiagnostic(`INSERT INTO js_stored VALUES(4,'"bad"',true)`, constraint)
		s.ErrorStage = "execute"
		jsonTypeBoth(t, p, b, c, s)
		jsonTypeBoth(t, p, b, c, check)
		jsonStripControls(t, p, b, c, check)
	})
}
