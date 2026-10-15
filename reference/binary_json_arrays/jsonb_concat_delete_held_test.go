package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		rhs       string
		arg       any
		doc, want string
	}{
		{"$2::text", "a", `{"a":1,"b":2}`, `{"b": 2}`},
		{"$2::integer", int64(-1), `[1,2]`, `[1]`},
		{"$2", "a", `{"a":1,"b":2}`, `{"b": 2}`},
	} {
		t.Run(tc.rhs, func(t *testing.T) {
			q := "SELECT $1::jsonb - " + tc.rhs + " AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
			good := jsonbPathDeleteMetadata(jsonbConcatDeleteSelect(q, tc.want, tc.doc, tc.arg), b.PG)
			run(good)
			for _, args := range [][]any{{nil, tc.arg}, {tc.doc, nil}, {nil, nil}} {
				run(jsonbPathDeleteMetadata(jsonbConcatDeleteSelect(q, nil, args...), b.PG))
				run(good)
			}
			run(jsonbConcatDeleteError(q, fixture.ControlsReferenceDiagnostic("22023", "cannot delete from scalar"), "null", tc.arg))
			run(good)
			if tc.rhs != "$2::integer" {
				run(jsonbConcatDeleteSelect(q, `[1]`, `["0",1]`, "0"))
				run(good)
			}
		})
	}
}

func TestReferenceJSONBConcatDeleteHeld(t *testing.T) {
	jsonbConcatDeleteScenario(t, jsonbConcatDeleteHeld)
}
