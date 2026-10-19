package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainDefaultStoredDeparseVisibility(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0)) }
		run("SET search_path TO public,pg_catalog")
		run("CREATE TYPE public.dd_visible_pair AS(a integer,b integer)")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP TYPE IF EXISTS public.dd_visible_pair CASCADE; DROP TYPE IF EXISTS public.dd_visible_pair_renamed CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{
			"CREATE TYPE domain_s.dd_hidden_pair AS(a integer,b integer)",
			"CREATE DOMAIN domain_s.dd_visible_default AS integer DEFAULT (ROW(7,8)::public.dd_visible_pair).a",
			"CREATE DOMAIN domain_s.dd_hidden_default AS integer DEFAULT (ROW(9,10)::domain_s.dd_hidden_pair).a",
			"CREATE DOMAIN domain_s.dd_visible_child AS domain_s.dd_visible_default",
		} {
			run(q)
		}
		// Released pg_type.typdefault is captured text: neither a changed
		// lookup path nor renamed providers rewrite an existing recipe.
		check := func(withHiddenChild bool) {
			t.Helper()
			want := [][]any{
				{"dd_hidden_default", "(ROW(9, 10)::domain_s.dd_hidden_pair).a"},
				{"dd_visible_child", "(ROW(7, 8)::dd_visible_pair).a"},
				{"dd_visible_default", "(ROW(7, 8)::dd_visible_pair).a"},
			}
			if withHiddenChild {
				want = append([][]any{{"dd_hidden_child", "(ROW(9, 10)::domain_s.dd_hidden_pair).a"}}, want...)
			}
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT t.typname,t.typdefault FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname", []string{"typname", "typdefault"}, []string{"NAME", "TEXT"}, want)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, r, step)
			}
		}
		check(false)
		run("SET search_path TO domain_s,pg_catalog")
		run("CREATE DOMAIN domain_s.dd_hidden_child AS domain_s.dd_hidden_default")
		check(true)
		for _, query := range []string{
			"ALTER TYPE public.dd_visible_pair RENAME TO dd_visible_pair_renamed",
			"ALTER TYPE domain_s.dd_hidden_pair RENAME TO dd_hidden_pair_renamed",
		} {
			if b.PG {
				// Released PG rename control; native type renaming is outside
				// this part's admitted utility grammar.
				run(query)
			} else {
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "TO"`)
				want.Position = fixture.Position(strings.Index(query, " TO ") + 2)
				for _, prepared := range []bool{false, true} {
					step := fixture.ErrorDiagnostic(query, want)
					step.Prepared, step.ErrorStage = prepared, "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, r, step)
				}
			}
			check(true)
		}
	})
}
