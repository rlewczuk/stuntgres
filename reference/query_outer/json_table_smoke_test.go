package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableFeatureSmoke(t *testing.T) {
	fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]' PASSING $2::int AS max COLUMNS(r FOR ORDINALITY,id int ERROR ON ERROR,formatted text FORMAT JSON PATH '$.id',found int EXISTS PATH '$.id',NESTED '$.a[*]?(@ < $max)' COLUMNS(ao FOR ORDINALITY,av int PATH '$' ERROR ON ERROR),NESTED '$.b[*]' COLUMNS(bo FOR ORDINALITY,bv text PATH '$')))`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := fixture.Select(q, []string{"r", "id", "formatted", "found", "ao", "av", "bo", "bv"}, []string{"INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{
			{int64(1), int64(7), `7`, int64(1), int64(1), int64(10), nil, nil},
			{int64(1), int64(7), `7`, int64(1), int64(2), int64(20), nil, nil},
			{int64(1), int64(7), `7`, int64(1), nil, nil, int64(1), `x`},
			{int64(2), int64(8), `8`, int64(1), nil, nil, nil, nil},
			{int64(3), int64(9), `9`, int64(1), nil, nil, int64(1), `z`},
		}, jsonTableSiblingDocument, 100)
		s.Bag = true
		fixture.SQLJSONBoth(t, p, b, c, jsonTableMetadata(s, b.PG))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonTableMetadata(s, b.PG))
		empty := s
		empty.Args = []any{`[]`, 100}
		empty.Rows = nil
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonTableMetadata(empty, b.PG))
		bad := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), `[{"id":1},{"id":"bad"}]`, 100)
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonTableMetadata(s, b.PG))
		// The same feature's eager late conversion is observable even when the
		// caller demands only a prefix and does not select the failing column.
		fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT r FROM JSON_TABLE($1::jsonb,'$[*]' COLUMNS(r FOR ORDINALITY,v int PATH '$' ERROR ON ERROR)) LIMIT 1`, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), false, `[1,"bad"]`)
		check := jsonTableStored(t, p, b, c)
		fixture.JSONPathNavigateReplay(t, p, b, c, check)
	})
}
