package reference_test

import "testing"

func TestReferenceBinarySliceKeywordNameContexts(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			for _, word := range []string{"both", "leading", "trailing", "placing"} {
				apply(t, referenceExec("CREATE TABLE public."+word+"(v BYTEA)", 0))
				apply(t, referenceExec("INSERT INTO public."+word+" VALUES('abc')", 1))
				for _, as := range []string{" AS ", " "} {
					step := referenceSelect("SELECT substring(v FROM 2)"+as+word+" FROM public."+word, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
					apply(t, step)
					apply(t, referencePrepared(step))
				}
			}
		})
	}
}

func TestReferenceBinarySliceFunctionKeywordNames(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			for _, word := range []string{"overlay", "position", "substring", "trim"} {
				apply(t, referenceExec("CREATE TABLE "+word+"("+word+" BYTEA)", 0))
				apply(t, referenceExec("INSERT INTO "+word+"("+word+") VALUES('abc')", 1))
				for _, table := range []string{word, "public." + word, `"` + word + `"`} {
					for _, label := range []string{" AS ", " "} {
						step := referenceSelect("SELECT substring("+word+" FROM 2)"+label+word+" FROM "+table, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
						apply(t, step)
						apply(t, referencePrepared(step))
					}
				}
			}
		})
	}
}
