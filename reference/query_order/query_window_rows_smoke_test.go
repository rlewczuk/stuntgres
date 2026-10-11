package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowRowsFeatureSmoke(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRowsSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowRowsResult("1 PRECEDING", 1, 2, 2, 2, 2))
		queryWindowRoutes(t, p, b, queryWindowRowsResult("BETWEEN 1 PRECEDING AND 2 PRECEDING", 0, 0, 0, 0, 0))
		queryWindowRangeRuntime(t, p, b, queryWindowRowsResult("NULL PRECEDING").Query, "22004", "frame starting offset must not be null")
		queryWindowRangeRuntime(t, p, b, queryWindowRowsResult("-1 PRECEDING").Query, "22013", "frame starting offset must not be negative")
		q := queryWindowRowsResult("$1 PRECEDING").Query
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, arg := range []any{1, nil, -1, 1} {
			s := queryWindowRangeResult(q, 1, 2, 2, 2, 2)
			s.Args = []any{arg}
			if arg == nil || arg == -1 {
				code, message := "22004", "frame starting offset must not be null"
				if arg == -1 {
					code, message = "22013", "frame starting offset must not be negative"
				}
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, arg)
				s.SelectRows = true
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		queryWindowRowsMetadata(t, p, b)
		queryWindowRowsViews(t, p, b)
	})
}
