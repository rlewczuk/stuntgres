package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIntaggUnqualifiedRecipes(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggInstall(run)
		run(fixture.Exec("CREATE TABLE public.ia_recipe_input(id integer,v integer,a integer[])", 0))
		run(fixture.Exec("INSERT INTO public.ia_recipe_input VALUES(1,2,'{2,2,NULL}'),(2,NULL,'{}'),(3,2,NULL)", 3))
		run(fixture.Exec("SET search_path=ia_addon,public", 0))
		run(fixture.Exec("CREATE VIEW public.ia_recipe_aggregate AS SELECT int_array_aggregate(v ORDER BY id) AS a FROM public.ia_recipe_input", 0))
		run(fixture.Exec("CREATE VIEW public.ia_recipe_from AS SELECT u.v,u.n FROM public.ia_recipe_input, int_array_enum(a) WITH ORDINALITY AS u(v,n)", 0))
		run(fixture.Exec("CREATE VIEW public.ia_recipe_target AS SELECT int_array_enum(a) AS v FROM public.ia_recipe_input", 0))
		run(fixture.Exec("SET search_path=public", 0))
		checks := []fixture.Step{
			intaggArray(b, "SELECT a FROM public.ia_recipe_aggregate", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,NULL,2}"}}),
			fixture.Select("SELECT v,n FROM public.ia_recipe_from ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(3)}}),
			fixture.Select("SELECT v FROM public.ia_recipe_target", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}}),
		}
		checks[2].Bag = true
		for _, s := range checks {
			fixture.ArrayAggBoth(run, s)
		}
		if b.PG {
			return
		} // Public native replacement APIs have no PG equivalent.
		dump := intaggDump(t, p.Ctx, p.Native)
		if !strings.Contains(dump, `"ia_addon"."int_array_aggregate"(`) || strings.Count(dump, `"ia_addon"."int_array_enum"(`) < 2 {
			t.Fatal("unqualified input lost selected installed procedure qualification", dump)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			fixture.ArrayAggBoth(run, s)
		}
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "intagg_unqualified_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			rows, err := other.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			intaggControlRows(t, rows, s)
		}
		if intaggDump(t, p.Ctx, other) != dump {
			t.Fatal("independent unqualified recipe replay changed")
		}
	})
}
