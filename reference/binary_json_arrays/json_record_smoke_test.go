package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordFeatureSmoke(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		input := `{"a":1,"b":[2,"a b"],"c":{"d":4,"e":"a b c"}}`
		for _, tc := range []struct {
			source string
			set    bool
		}{
			{`json_populate_record(NULL::jr_outer,$1::json)`, false},
			{`json_populate_recordset(NULL::jr_outer,$1::json)`, true},
			{`json_to_record($1::json) AS r(a int,b text[],c jr_sub)`, false},
			{`json_to_recordset($1::json) AS r(a int,b text[],c jr_sub)`, true},
		} {
			value := input
			if tc.set {
				value = "[" + value + "]"
			}
			s := fixture.Select(`SELECT a,b::text AS b,c::text AS c FROM `+tc.source, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), `{2,"a b"}`, `(4,"a b c")`}}, value)
			jsonRecordBoth(t, p, b, c, jsonRecordMetadata(s, b.PG))
		}
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT json_populate_record(NULL::jr_pair,'{"a":2}') AS r`, []string{"r"}, []string{""}, [][]any{{"(2,)"}}))
		q := `SELECT * FROM json_populate_record($1::jr_pair,$2::json)`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonRecordPair(q, [][]any{{int64(2), "keep"}}, "(7,keep)", `{"a":"2"}`))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonRecordPair(q, [][]any{{int64(7), nil}}, "(7,keep)", `{"b":null}`))
		jsonKeysError(t, p, b, held, q, false, jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0), "execute", "(7,keep)", `{"a":"bad"}`)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonRecordPair(q, [][]any{{int64(3), "keep"}}, "(7,keep)", `{"a":3}`))
		mixed := fixture.Select(`SELECT * FROM ROWS FROM(json_to_recordset('[{"a":1},{"a":2}]') AS (a int,b text),generate_series(10,12)) WITH ORDINALITY AS r(a,b,g,n) ORDER BY n`, []string{"a", "b", "g", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), nil, int64(10), int64(1)}, {int64(2), nil, int64(11), int64(2)}, {nil, nil, int64(12), int64(3)}})
		jsonRecordBoth(t, p, b, c, jsonRecordMetadata(mixed, b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jr_smoke_destination(a int,b text)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jr_smoke_destination VALUES(99,'sentinel')`, 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO jr_smoke_destination SELECT * FROM json_to_recordset($1::json) AS r(a int,b text)`, jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0), `[{"a":1},{"a":"bad"}]`)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, jsonRecordPair(`SELECT a,b FROM jr_smoke_destination`, [][]any{{int64(99), "sentinel"}}))
		}
		checks := jsonRecordControlSetup(t, p, b, c)
		jsonRecordControlRoundtrip(t, p, b, c, checks)
	})
}
