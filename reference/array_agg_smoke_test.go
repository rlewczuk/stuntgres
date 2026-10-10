package reference_test

import "testing"

func TestReferenceArrayAggFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			arrayAggSetup(run)
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_scalar", "_INT4", [][]any{{"{3,NULL,1,NULL,3}"}}, b.pg))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(DISTINCT v ORDER BY v) FROM aa_scalar", "_INT4", [][]any{{"{1,3,NULL}"}}, b.pg))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_array", "_INT4", [][]any{{"{{1,NULL},{3,4}}"}}, b.pg))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg('[0:1]={1,NULL}'::integer[])", "_INT4", [][]any{{"[1:1][0:1]={{1,NULL}}"}}, b.pg))
			q := "SELECT array_agg(v ORDER BY id) FROM aa_scalar WHERE id>$1::integer"
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
			for _, tc := range []struct {
				arg  int64
				want any
			}{{5, nil}, {0, "{3,NULL,1,NULL,3}"}, {5, nil}} {
				referenceRunStep(t, p.ctx, held, b.recorder, arrayAggSelect(q, "_INT4", [][]any{{tc.want}}, b.pg, tc.arg))
			}
			run(referenceExec("INSERT INTO aa_array VALUES(3,NULL)", 1))
			for _, prepared := range []bool{false, true} {
				arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_array", prepared, referenceDiagnostic{Code: "22004", Message: "cannot accumulate null arrays"})
			}
			arrayAggAtomicity(t, p, b, run)
			s := referenceImplicitError("SELECT array_agg()", referenceDiagnostic{Code: "42883", Message: "function array_agg() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
			s.selectRows = true
			arrayAggBoth(run, s)
		})
	}
}
