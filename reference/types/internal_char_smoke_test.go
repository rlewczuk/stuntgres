package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInternalCharFeatureSmoke(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_smoke(id integer,v pg_catalog."char")`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_smoke VALUES(1,'r'),(2,''),(3,NULL)`, 3))
		want := [][]any{{"r"}, {""}, {nil}}
		internalCharControlRows(t, p, b, b.DB, "internal_char_smoke", want)
		q := `SELECT $1::pg_catalog."char" AS v`
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ input, want any }{
			{"r", "r"}, {"abc", "a"}, {"é", `\303`}, {"", ""}, {nil, nil},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, internalCharSelect(q, [][]any{{frame.want}}, frame.input))
		}
		s := fixture.ErrorDiagnostic(q, internalCharEncodingDiagnostic("0xff", 1), "a\xff")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, internalCharSelect(q, [][]any{{"r"}}, "r"))
		run(internalCharSelect(`SELECT (-1)::int4::"char" AS v`, [][]any{{`\377`}}))
		if !b.PG {
			internalCharNativeRoundTrip(t, p, b, "internal_char_smoke", want)
		}
	})
}
