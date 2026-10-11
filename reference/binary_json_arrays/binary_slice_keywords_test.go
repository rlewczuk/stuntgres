package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceKeywordNameContexts(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			for _, word := range []string{"both", "leading", "trailing", "placing"} {
				apply(t, fixture.Exec("CREATE TABLE public."+word+"(v BYTEA)", 0))
				apply(t, fixture.Exec("INSERT INTO public."+word+" VALUES('abc')", 1))
				for _, as := range []string{" AS ", " "} {
					step := fixture.Select("SELECT substring(v FROM 2)"+as+word+" FROM public."+word, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
					apply(t, step)
					apply(t, fixture.Prepared(step))
				}
			}
		})
	}
}

func TestReferenceBinarySliceFunctionKeywordNames(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			for _, word := range []string{"overlay", "position", "substring", "trim"} {
				apply(t, fixture.Exec("CREATE TABLE "+word+"("+word+" BYTEA)", 0))
				apply(t, fixture.Exec("INSERT INTO "+word+"("+word+") VALUES('abc')", 1))
				for _, table := range []string{word, "public." + word, `"` + word + `"`} {
					for _, label := range []string{" AS ", " "} {
						step := fixture.Select("SELECT substring("+word+" FROM 2)"+label+word+" FROM "+table, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
						apply(t, step)
						apply(t, fixture.Prepared(step))
					}
				}
			}
		})
	}
}
