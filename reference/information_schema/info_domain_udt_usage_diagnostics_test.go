package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageDiagnostics(t *testing.T) {
	for _, phase := range []string{"view_restrictions", "dependency_restrict"} {
		t.Run(phase, func(t *testing.T) {
			duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if phase == "dependency_restrict" {
					var notices []fixture.Diagnostic
					_, noticeConn, noticeRecorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer noticeConn.Close()
					c, r = noticeConn, noticeRecorder
					run = func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
					for _, q := range []string{"CREATE DOMAIN public.du_restrict AS integer", "CREATE DOMAIN public.du_child AS public.du_restrict", "CREATE TABLE public.du_restrict_t(v integer,keep integer)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO public.du_restrict_t VALUES(7,9)", 1))
					want := [][]any{duRow(database, "pg_catalog", "int4", "public", "du_restrict"), duRow(database, "public", "du_restrict", "public", "du_child")}
					s := duProjection(b.PG, "domain_schema='public' AND domain_name IN ('du_restrict','du_child')", want)
					held := cduHold(t, p, c, s.Query)
					defer held.Statement.Close()
					d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type du_restrict because other objects depend on it")
					d.Detail = "type du_child depends on type du_restrict"
					d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic("DROP DOMAIN public.du_restrict RESTRICT", d)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						run(failure)
						duCheck(t, p, c, r, held, s)
						run(fixture.Select("SELECT v,keep FROM public.du_restrict_t", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
						run(fixture.Select("SELECT typname FROM pg_catalog.pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('du_restrict','du_child') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"du_child"}, {"du_restrict"}}))
					}
					run(fixture.Exec("DROP DOMAIN public.du_restrict CASCADE", 0))
					fixture.DomainReferenceNotice(t, &notices, "drop cascades to type du_child", "")
					duCheck(t, p, c, r, held, duProjection(b.PG, "domain_schema='public' AND domain_name IN ('du_restrict','du_child')", nil))
					run(fixture.Select("SELECT * FROM public.du_restrict_t", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
					return
				}
				duSeed(run)
				s := duProjection(b.PG, "domain_schema='du_scope'", duSeedRows(database))
				held := cduHold(t, p, c, s.Query)
				run(fixture.Exec("CREATE TABLE du_scope.sentinel(v integer)", 0))
				run(fixture.Exec("INSERT INTO du_scope.sentinel VALUES(91)", 1))
				check := func() {
					duCheck(t, p, c, r, held, s)
					run(fixture.Select("SELECT v FROM du_scope.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					run(fixture.Select("SELECT typname FROM pg_catalog.pg_type WHERE typnamespace='du_scope'::regnamespace AND typtype='d' ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"arr"}, {"c"}, {"de"}, {"dr"}, {"p"}}))
				}
				check()
				for _, query := range []string{"SELECT missing FROM information_schema.domain_udt_usage", "/*é*/ SELECT missing FROM information_schema.domain_udt_usage"} {
					d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
					d.Position = strconv.Itoa(len([]rune(query[:strings.Index(query, "missing")])) + 1)
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic(query, d)
						failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
						if prepared {
							failure.ErrorStage = "prepare"
						}
						run(failure)
						check()
					}
				}
				for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
					q := map[string]string{"INSERT": "INSERT INTO information_schema.domain_udt_usage(domain_name) VALUES('fake')", "UPDATE": "UPDATE information_schema.domain_udt_usage SET domain_name='fake'", "DELETE": "DELETE FROM information_schema.domain_udt_usage"}[event]
					d := fixture.ControlsReferenceDiagnostic("55000", map[string]string{"INSERT": `cannot insert into view "domain_udt_usage"`, "UPDATE": `cannot update view "domain_udt_usage"`, "DELETE": `cannot delete from view "domain_udt_usage"`}[event])
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
				for _, q := range []string{"INSERT INTO information_schema.domain_udt_usage(missing) VALUES('fake')", "UPDATE information_schema.domain_udt_usage SET missing='fake' WHERE FALSE", "DELETE FROM information_schema.domain_udt_usage WHERE missing='fake'"} {
					message := `column "missing" does not exist`
					if !strings.HasPrefix(q, "DELETE") {
						message = `column "missing" of relation "domain_udt_usage" does not exist`
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
