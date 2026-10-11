package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Prospective independent PostgreSQL expectations: root validates these in the
// pinned oracle before accepting native renderer results.
func TestReferenceArrayDomainDeparse(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN public.array_render_whole AS INT[]", 0))
		run(fixture.Exec("CREATE DOMAIN public.array_render_chain AS public.array_render_whole", 0))
		names := []string{"array_render_whole", "array_render_chain"}
		var check fixture.Step
		for _, name := range names {
			constraint := name + "_check"
			table := name + "_table"
			run(fixture.Exec("CREATE TABLE public."+table+"(a INT[],CONSTRAINT "+constraint+" CHECK(a::public."+name+" IS NOT NULL))", 0))
			run(fixture.Exec("INSERT INTO public."+table+" VALUES('{}'),('[0:1]={7,NULL}')", 2))
			check = fixture.ArrayReferenceSelect("SELECT a FROM public."+table, []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{}"}, {"[0:1]={7,NULL}"}}, b.PG)
			run(check)
			run(fixture.Prepared(check))
		}
		checkDefinitions := func() {
			for _, name := range names {
				constraint := name + "_check"
				for _, mode := range []struct{ flag, expression string }{
					{"false", "((a)::" + name + " IS NOT NULL)"},
					{"true", "a::" + name + " IS NOT NULL"},
				} {
					step := fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.expression}})
					run(step)
					run(fixture.Prepared(step))
					step = fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.expression + ")"}})
					run(step)
					run(fixture.Prepared(step))
				}
			}
		}
		checkDefinitions()
		if !b.PG {
			// Replay the stored casts through Dump/Restore, then require the
			// restored deparser to retain both logical domain names again.
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
			checkDefinitions()
		}
	})
}
