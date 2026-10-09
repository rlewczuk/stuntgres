package reference_test

import (
	"database/sql"
	"testing"
)

// Prospective independent PostgreSQL expectations: root validates these in the
// pinned oracle before accepting native renderer results.
func TestReferenceArrayDomainDeparse(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN public.array_render_whole AS INT[]", 0))
		run(referenceExec("CREATE DOMAIN public.array_render_chain AS public.array_render_whole", 0))
		names := []string{"array_render_whole", "array_render_chain"}
		var check referenceStep
		for _, name := range names {
			constraint := name + "_check"
			table := name + "_table"
			run(referenceExec("CREATE TABLE public."+table+"(a INT[],CONSTRAINT "+constraint+" CHECK(a::public."+name+" IS NOT NULL))", 0))
			run(referenceExec("INSERT INTO public."+table+" VALUES('{}'),('[0:1]={7,NULL}')", 2))
			check = arrayReferenceSelect("SELECT a FROM public."+table, []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{}"}, {"[0:1]={7,NULL}"}}, b.pg)
			run(check)
			run(referencePrepared(check))
		}
		checkDefinitions := func() {
			for _, name := range names {
				constraint := name + "_check"
				for _, mode := range []struct{ flag, expression string }{
					{"false", "((a)::" + name + " IS NOT NULL)"},
					{"true", "a::" + name + " IS NOT NULL"},
				} {
					step := referenceSelect("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.expression}})
					run(step)
					run(referencePrepared(step))
					step = referenceSelect("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.expression + ")"}})
					run(step)
					run(referencePrepared(step))
				}
			}
		}
		checkDefinitions()
		if !b.pg {
			// Replay the stored casts through Dump/Restore, then require the
			// restored deparser to retain both logical domain names again.
			arrayReferenceNativeRoundtrip(t, p, c, b, check)
			checkDefinitions()
		}
	})
}
