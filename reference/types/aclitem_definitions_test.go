package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceACLItemDefinitions(t *testing.T) {
	roles := []string{"acl_definition_a", "acl_definition_b"}
	aclitemScenario(t, roles, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_definition (id INT,a aclitem DEFAULT 'acl_definition_a=r*/acl_definition_b',CHECK (a='acl_definition_a=r*/acl_definition_b'::aclitem))", 0))
		run(fixture.Exec("CREATE INDEX acl_definition_expression ON public.acl_definition ((CASE WHEN a='acl_definition_a=r*/acl_definition_b'::aclitem THEN 1 ELSE 2 END))", 0))
		run(fixture.Exec("CREATE VIEW public.acl_definition_view AS SELECT 'acl_definition_a=r*/acl_definition_b'::aclitem AS a", 0))
		run(fixture.Exec("INSERT INTO public.acl_definition (id) VALUES (1)", 1))
		check := fixture.Select("SELECT id,a FROM public.acl_definition ORDER BY id", []string{"id", "a"}, []string{"INT4", "ACLITEM"}, [][]any{{int64(1), "acl_definition_a=r*/acl_definition_b"}})
		run(check)
		run(fixture.Prepared(check))
		run(aclitemSelect("SELECT a FROM public.acl_definition_view", [][]any{{"acl_definition_a=r*/acl_definition_b"}}))
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check, fixture.Exec("CREATE ROLE acl_definition_spacer", 0), fixture.Exec("CREATE ROLE acl_definition_a", 0), fixture.Exec("CREATE ROLE acl_definition_b", 0))
			run(aclitemSelect("SELECT a FROM public.acl_definition_view", [][]any{{"acl_definition_a=r*/acl_definition_b"}}))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			target := fixture.NewFixture(t, false, true)
			runTarget := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
			for _, role := range []string{"acl_definition_target_spacer", "acl_definition_target_spacer2", "acl_definition_a", "acl_definition_b"} {
				runTarget(fixture.Exec("CREATE ROLE "+role, 0))
			}
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			runTarget(fixture.Prepared(fixture.Exec("INSERT INTO public.acl_definition (id) VALUES (2)", 1)))
			runTarget(fixture.Select("SELECT id,a,a=$1::aclitem AS same FROM public.acl_definition ORDER BY id", []string{"id", "a", "same"}, []string{"INT4", "ACLITEM", "BOOL"}, [][]any{{int64(1), "acl_definition_a=r*/acl_definition_b", true}, {int64(2), "acl_definition_a=r*/acl_definition_b", true}}, "acl_definition_a=r*/acl_definition_b"))
			runTarget(fixture.Select("SELECT a=$1::aclitem AS same FROM public.acl_definition_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, "acl_definition_a=r*/acl_definition_b"))
			// A fresh bound value exercises the imported CHECK and expression
			// index using independently allocated target principal identities.
			runTarget(fixture.Prepared(fixture.Exec("INSERT INTO public.acl_definition VALUES (3,$1::aclitem)", 1, "acl_definition_a=r*/acl_definition_b")))
		}
		// Defaults, checks and the expression-index provider all execute after
		// reownership, not just render the original source text successfully.
		run(fixture.Prepared(fixture.Exec("INSERT INTO public.acl_definition (id) VALUES (2)", 1)))
		run(fixture.Select("SELECT a=$1::aclitem AS same FROM public.acl_definition ORDER BY id", []string{"same"}, []string{"BOOL"}, [][]any{{true}, {true}}, "acl_definition_a=r*/acl_definition_b"))
	})
}
