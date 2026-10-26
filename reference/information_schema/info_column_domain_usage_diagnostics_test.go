package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsageDiagnostics(t *testing.T) {
	for _, phase := range []string{"view_restrictions", "dependency_restrict"} {
		t.Run(phase, func(t *testing.T) {
			cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if phase == "dependency_restrict" {
					for _, q := range []string{"CREATE DOMAIN public.cdu_restrict AS integer", "CREATE TABLE public.cdu_restrict_t(v public.cdu_restrict,keep integer)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO public.cdu_restrict_t VALUES(7,9)", 1))
					want := [][]any{cduRow(database, "public", "cdu_restrict", "public", "cdu_restrict_t", "v")}
					s := cduProjection(b.PG, "table_schema='public' AND table_name='cdu_restrict_t'", want)
					held := cduHold(t, p, c, s.Query)
					d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type cdu_restrict because other objects depend on it")
					d.Detail = "column v of table cdu_restrict_t depends on type cdu_restrict"
					d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic("DROP DOMAIN public.cdu_restrict RESTRICT", d)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						run(failure)
						cduCheck(t, p, c, r, held, s)
						run(fixture.Select("SELECT v,keep FROM public.cdu_restrict_t", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
					}
					run(fixture.Exec("DROP DOMAIN public.cdu_restrict CASCADE", 0))
					cduCheck(t, p, c, r, held, cduProjection(b.PG, "table_schema='public' AND table_name='cdu_restrict_t'", nil))
					run(fixture.Select("SELECT * FROM public.cdu_restrict_t", []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
					return
				}
				cduSeed(run)
				s := cduProjection(b.PG, "table_schema='cdu'", cduSeedRows(database))
				held := cduHold(t, p, c, s.Query)
				check := func() { cduCheck(t, p, c, r, held, s) }
				check()
				cduMissing(func(s fixture.Step) { run(s); check() })
				for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
					q := map[string]string{"INSERT": "INSERT INTO information_schema.column_domain_usage(table_name) VALUES('fake')", "UPDATE": "UPDATE information_schema.column_domain_usage SET table_name='fake'", "DELETE": "DELETE FROM information_schema.column_domain_usage"}[event]
					d := fixture.ControlsReferenceDiagnostic("55000", map[string]string{"INSERT": `cannot insert into view "column_domain_usage"`, "UPDATE": `cannot update view "column_domain_usage"`, "DELETE": `cannot delete from view "column_domain_usage"`}[event])
					d.Detail = "Views that do not select from a single table or view are not automatically updatable."
					d.Hint = "To enable " + map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[event] + " the view, provide an INSTEAD OF " + event + " trigger or an unconditional ON " + event + " DO INSTEAD rule."
					queries := []string{q}
					if event != "INSERT" {
						queries = append(queries, q+" WHERE FALSE")
					}
					for _, query := range queries {
						for _, prepared := range []bool{false, true} {
							failure := fixture.ErrorDiagnostic(query, d)
							failure.Prepared, failure.ErrorStage = prepared, "execute"
							if prepared {
								failure.ErrorStage = "prepare"
							}
							run(failure)
							check()
						}
					}
				}
				// Target binding errors precede the nonupdatable-view restriction.
				for _, q := range []string{"INSERT INTO information_schema.column_domain_usage(missing) VALUES('fake')", "UPDATE information_schema.column_domain_usage SET missing='fake' WHERE FALSE", "DELETE FROM information_schema.column_domain_usage WHERE missing='fake'"} {
					message := `column "missing" does not exist`
					if !strings.HasPrefix(q, "DELETE") {
						message = `column "missing" of relation "column_domain_usage" does not exist`
					}
					d := fixture.ControlsReferenceDiagnostic("42703", message)
					d.Position = strconv.Itoa(strings.Index(q, "missing") + 1)
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic(q, d)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						if prepared {
							failure.ErrorStage = "prepare"
						}
						run(failure)
						check()
					}
				}
			})
		})
	}
}
