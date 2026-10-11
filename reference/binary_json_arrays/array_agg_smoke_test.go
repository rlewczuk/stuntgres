package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayAggFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			arrayAggSetup(run)
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_scalar", "_INT4", [][]any{{"{3,NULL,1,NULL,3}"}}, b.PG))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(DISTINCT v ORDER BY v) FROM aa_scalar", "_INT4", [][]any{{"{1,3,NULL}"}}, b.PG))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_array", "_INT4", [][]any{{"{{1,NULL},{3,4}}"}}, b.PG))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg('[0:1]={1,NULL}'::integer[])", "_INT4", [][]any{{"[1:1][0:1]={{1,NULL}}"}}, b.PG))
			q := "SELECT array_agg(v ORDER BY id) FROM aa_scalar WHERE id>$1::integer"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				arg  int64
				want any
			}{{5, nil}, {0, "{3,NULL,1,NULL,3}"}, {5, nil}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayAggSelect(q, "_INT4", [][]any{{tc.want}}, b.PG, tc.arg))
			}
			run(fixture.Exec("INSERT INTO aa_array VALUES(3,NULL)", 1))
			for _, prepared := range []bool{false, true} {
				arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_array", prepared, fixture.Diagnostic{Code: "22004", Message: "cannot accumulate null arrays"})
			}
			arrayAggAtomicity(t, p, b, run)
			s := fixture.ImplicitError("SELECT array_agg()", fixture.Diagnostic{Code: "42883", Message: "function array_agg() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
			s.SelectRows = true
			arrayAggBoth(run, s)
		})
	}
}
