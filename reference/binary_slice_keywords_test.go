//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceKeywordNameContexts(t *testing.T) {
	p := newReferencePair(t)
	for _, word := range []string{"both", "leading", "trailing", "placing"} {
		p.step(t, referenceExec("CREATE TABLE public."+word+"(v BYTEA)", 0))
		p.step(t, referenceExec("INSERT INTO public."+word+" VALUES('abc')", 1))
		for _, as := range []string{" AS ", " "} {
			step := referenceSelect("SELECT substring(v FROM 2)"+as+word+" FROM public."+word, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
}

func TestReferenceBinarySliceFunctionKeywordNames(t *testing.T) {
	p := newReferencePair(t)
	for _, word := range []string{"overlay", "position", "substring", "trim"} {
		p.step(t, referenceExec("CREATE TABLE "+word+"("+word+" BYTEA)", 0))
		p.step(t, referenceExec("INSERT INTO "+word+"("+word+") VALUES('abc')", 1))
		for _, table := range []string{word, "public." + word, `"` + word + `"`} {
			for _, label := range []string{" AS ", " "} {
				step := referenceSelect("SELECT substring("+word+" FROM 2)"+label+word+" FROM "+table, []string{word}, []string{"BYTEA"}, [][]any{{"bc"}})
				p.step(t, step)
				p.step(t, referencePrepared(step))
			}
		}
	}
}
