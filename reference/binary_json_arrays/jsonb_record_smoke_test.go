package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "jbr_saved_schema")
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		checks := jsonbRecordStored(t, p, b, c)
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_recordset('(9,keep)'::jbr_saved_schema.jbr_saved_renamed,'[{"a":1},{"b":"x"},{}]'::jsonb) WITH ORDINALITY AS r(a,b,n) ORDER BY n`, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "keep", int64(1)}, {int64(9), "x", int64(2)}, {int64(9), "keep", int64(3)}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record('(9,keep)'::jbr_saved_schema.jbr_saved_renamed,'{"a":null}'::jsonb)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "keep"}}))
		q := `SELECT * FROM jsonb_to_record($1::jsonb) AS r(a int,b text)`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input string
			row   []any
		}{{`{"a":2,"b":"two"}`, []any{int64(2), "two"}}, {`{}`, []any{nil, nil}}, {`{"a":3,"b":"three"}`, []any{int64(3), "three"}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{tc.row}, tc.input))
			if tc.input == `{}` {
				jsonKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), "execute", `{"a":"bad"}`)
			}
		}
		stmt.Close()
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_to_record('{"a":1,"b":"x","c":12.34}'::jsonb) AS r(a int,b text,c numeric(4,1))`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(1), "x", "12.3"}}))
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_smoke_char AS (c char(2))`)
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(NULL::jbr_smoke_char,'{"c":"aa"}'::jsonb) AS good,jsonb_populate_record_valid(NULL::jbr_smoke_char,'{"c":"aaa"}'::jsonb) AS bad`, []string{"good", "bad"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		for _, prepared := range []bool{false, true} {
			jsonKeysError(t, p, b, c, `SELECT jsonb_populate_record(NULL::jbr_smoke_char,'{"c":"aaa"}'::jsonb)`, prepared, fixture.ControlsReferenceDiagnostic("22001", "value too long for type character(2)"), "execute")
		}
		failure := fixture.ErrorDiagnostic(`INSERT INTO jbr_saved_rows SELECT 99,'(1,late)' FROM jsonb_populate_recordset(NULL::jbr_saved_schema.jbr_saved_renamed,'[{"a":1},{"a":"bad"}]'::jsonb) r`, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		jsonbRecordControls(t, p, b, c, checks)
	})
}
