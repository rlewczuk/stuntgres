package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBStripNullsFeatureSmoke(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_smoke(id int,v jsonb)", 0))
		jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO jbs_smoke VALUES(1,$1),(2,'null'),(3,NULL)", 3, jsonbStripInput)))
		for _, tc := range []struct{ option, want string }{{"", jsonbStripDefault}, {",true", jsonbStripArrays}} {
			s := fixture.Select("SELECT jsonb_strip_nulls(v"+tc.option+") AS stripped FROM jbs_smoke ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{tc.want}, {"null"}, {nil}})
			jsonTypeBoth(t, p, b, c, jsonbStripMetadata(s, b.PG))
		}
		q := "SELECT jsonb_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ flag, want any }{{true, jsonbStripArrays}, {false, jsonbStripDefault}, {nil, nil}} {
				jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, tc.want, jsonbStripInput, tc.flag), b.PG))
			}
			for _, tc := range []struct{ input, flag, want any }{{nil, true, nil}, {"null", true, "null"}} {
				jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, tc.want, tc.input, tc.flag), b.PG))
			}
			bad := jsonTypeError(q, "22P02", "invalid input syntax for type json", 0, "oops", nil)
			bad.Diagnostic.Detail = `Token "oops" is invalid.`
			bad.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $1 = '...'"
			jsonTypeRun(t, p, b, held, bad)
			jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, jsonbStripArrays, jsonbStripInput, true), b.PG))
			for _, target := range []any{jsonbStripInput, nil} {
				badFlag := jsonTypeError(q, "22P02", `invalid input syntax for type boolean: "truth"`, 0, target, "truth")
				badFlag.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
				jsonTypeRun(t, p, b, held, badFlag)
				for _, recovery := range []struct{ flag, want any }{{true, jsonbStripArrays}, {false, jsonbStripDefault}, {nil, nil}} {
					jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, recovery.want, jsonbStripInput, recovery.flag), b.PG))
				}
			}
		}()
		jsonbStripHeldOwnership(t, p, b, c)
		jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT jsonb_strip_nulls(1)", "jsonb_strip_nulls(integer)", 8))
		bad := jsonTypeError("SELECT jsonb_strip_nulls('oops')", "22P02", "invalid input syntax for type json", 26)
		bad.Diagnostic.Detail, bad.Diagnostic.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
		jsonTypeAnalysisError(t, p, b, c, bad)
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE INDEX jbs_smoke_index ON jbs_smoke ((jsonb_strip_nulls(v)))", 0))
		volatile := jsonTypeError("CREATE INDEX jbs_smoke_volatile ON jbs_smoke ((jsonb_strip_nulls(v,(random()>0.5)::boolean)))", "42P17", "functions in index expression must be marked IMMUTABLE", 0)
		volatile.SelectRows = false
		jsonTypeBoth(t, p, b, c, volatile)
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE INDEX jbs_smoke_volatile ON jbs_smoke ((jsonb_strip_nulls(v,true)))", 0))
		check := jsonbStripStored(t, p, b, c)
		jsonbStripFreshWrites(t, p, b, c)
		constraint := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbs_stored" violates check constraint "jbs_clean"`)
		constraint.Detail = `Failing row contains (4, "bad", t).`
		constraint.Schema, constraint.Table, constraint.Constraint = "public", "jbs_stored", "jbs_clean"
		s := fixture.ErrorDiagnostic(`INSERT INTO jbs_stored VALUES(5,'{"ok":null}',true),(4,'"bad"',true)`, constraint)
		s.ErrorStage = "execute"
		jsonTypeBoth(t, p, b, c, s)
		jsonTypeBoth(t, p, b, c, check)
		jsonbStripControls(t, p, b, c, check)
		jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO jbs_smoke VALUES(4,$1)", 1, jsonbStripInput)))
		jsonTypeBoth(t, p, b, c, jsonbStripMetadata(jsonbStripSelect("SELECT jsonb_strip_nulls(v,true) AS stripped FROM jbs_smoke WHERE id=4", jsonbStripArrays), b.PG))
		jsonTypeRun(t, p, b, c, fixture.Exec("DELETE FROM jbs_smoke WHERE id=4", 1))
	})
}
