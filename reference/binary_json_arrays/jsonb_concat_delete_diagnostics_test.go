package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteDiagnostics(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, doc := range []string{`1`, `"s"`, `true`, `false`, `null`} {
			for _, rhs := range []struct {
				typ     string
				arg     any
				literal string
			}{{"text", "x", "'x'::text"}, {"integer", int64(0), "0"}} {
				t.Run(doc+rhs.typ, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("22023", "cannot delete from scalar")
					q := "SELECT $1::jsonb - $2::" + rhs.typ + " AS v"
					jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError(q, d, doc, rhs.arg))
					literal := "SELECT " + jsonTypeQuote(doc) + "::jsonb - " + rhs.literal + " AS v"
					jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError(literal, d))
				})
			}
		}
		for _, doc := range []string{`{}`, `{"a":1}`} {
			d := fixture.ControlsReferenceDiagnostic("22023", "cannot delete from object using integer index")
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError("SELECT $1::jsonb - $2::integer AS v", d, doc, int64(0)))
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError("SELECT "+jsonTypeQuote(doc)+"::jsonb - 0 AS v", d))
		}
		for _, tc := range []struct {
			q, signature string
			pos          int
		}{
			{`SELECT '{}'::jsonb - TRUE`, "jsonb - boolean", 20},
			{`SELECT '{}'::jsonb - 1::bigint`, "jsonb - bigint", 20},
			{`SELECT '{}'::jsonb - 1::numeric`, "jsonb - numeric", 20},
			{`SELECT '{}'::jsonb || 1`, "jsonb || integer", 20},
			{`SELECT /*é*/ '{}'::jsonb - TRUE`, "jsonb - boolean", 26},
			{`SELECT NULL::jsonb - TRUE WHERE FALSE`, "jsonb - boolean", 20},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
				d.Position = fixture.Position(tc.pos)
				d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				jsonTypeAnalysisError(t, p, b, c, jsonbConcatDeleteError(tc.q, d))
			})
		}
		for _, q := range []string{`SELECT '1' - '1'`, `SELECT $1 - $2`} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown - unknown")
				d.Position = "12"
				if q == `SELECT $1 - $2` {
					d.Position = "11"
				}
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				s := jsonbConcatDeleteError(q, d)
				if q == `SELECT $1 - $2` {
					s.Args = []any{"1", "1"}
				}
				jsonTypeAnalysisError(t, p, b, c, s)
			})
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbcd_empty(doc jsonb)", 0))
		d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: jsonb - boolean")
		d.Position, d.Hint = "12", "No operator matches the given name and argument types. You might need to add explicit type casts."
		jsonTypeAnalysisError(t, p, b, c, jsonbConcatDeleteError("SELECT doc - TRUE FROM jbcd_empty", d))
		// Constants can fail at execution planning even if the filter is false;
		// bound parameters in a held plan are probed separately below.
		t.Run("constant_false_filter", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("22023", "cannot delete from scalar")
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError(`SELECT '1'::jsonb - 'x'::text AS v WHERE FALSE`, d))
		})
		t.Run("bound_false_filter", func(t *testing.T) {
			q := "SELECT $1::jsonb - $2::text AS v WHERE FALSE"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			if b.PG {
				d := fixture.ControlsReferenceDiagnostic("22023", "cannot delete from scalar")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbConcatDeleteError(q, d, "1", "x"))
			} else {
				// Native parameters remain runtime-owned; WHERE FALSE does not
				// demand the provider. PostgreSQL custom plans fold these binds.
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"JSONB"}, nil, "1", "x"))
			}
		})
	})
}
