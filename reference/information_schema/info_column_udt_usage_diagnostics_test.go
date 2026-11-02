package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func cuMissing(run func(fixture.Step)) {
	for _, q := range []string{"SELECT missing FROM information_schema.column_udt_usage", "/*é*/ SELECT missing FROM information_schema.column_udt_usage"} {
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
	}
}

func cuReadOnly(run func(fixture.Step), events []string) {
	for _, event := range events {
		q := map[string]string{"INSERT": "INSERT INTO information_schema.column_udt_usage(table_name) VALUES('fake')", "UPDATE": "UPDATE information_schema.column_udt_usage SET table_name='fake'", "DELETE": "DELETE FROM information_schema.column_udt_usage"}[event]
		d := fixture.ControlsReferenceDiagnostic("55000", map[string]string{"INSERT": `cannot insert into view "column_udt_usage"`, "UPDATE": `cannot update view "column_udt_usage"`, "DELETE": `cannot delete from view "column_udt_usage"`}[event])
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
			}
		}
	}
}

func TestReferenceInfoColumnUdtUsageDiagnostics(t *testing.T) {
	for _, phase := range []string{"view_restrictions", "domain_restrict", "enum_restrict"} {
		t.Run(phase, func(t *testing.T) {
			cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if phase != "view_restrictions" {
					kind, name, declaration, value, projectedSchema, projectedType, transport := "DOMAIN", "cu_restrict", "AS integer", "7", "pg_catalog", "int4", "INT4"
					if phase == "enum_restrict" {
						kind, name, declaration, value, projectedSchema, projectedType, transport = "TYPE", "cu_restrict_enum", "AS ENUM('x')", "'x'", "public", "cu_restrict_enum", "TEXT"
					}
					table := name + "_t"
					for _, q := range []string{"CREATE " + kind + " public." + name + " " + declaration, "CREATE TABLE public." + table + "(v public." + name + ",keep integer)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO public."+table+" VALUES("+value+",9)", 1))
					want := [][]any{cuRow(database, projectedSchema, projectedType, "public", table, "v"), cuRow(database, "pg_catalog", "int4", "public", table, "keep")}
					where := "table_schema='public' AND table_name='" + table + "'"
					s := cuProjection(b.PG, where, want)
					held := cduHold(t, p, c, s.Query)
					d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type "+name+" because other objects depend on it")
					d.Detail = "column v of table " + table + " depends on type " + name
					d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic("DROP "+kind+" public."+name+" RESTRICT", d)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						run(failure)
						cuCheck(t, p, c, r, held, s)
						var stored any = int64(7)
						if phase == "enum_restrict" {
							stored = "x"
						}
						projection := "v"
						if phase == "enum_restrict" {
							projection = "v::text AS v"
						}
						data := fixture.Select("SELECT "+projection+",keep FROM public."+table, []string{"v", "keep"}, []string{transport, "INT4"}, [][]any{{stored, int64(9)}})
						run(data)
						run(fixture.Prepared(data))
					}
					run(fixture.Exec("DROP "+kind+" public."+name+" CASCADE", 0))
					cuCheck(t, p, c, r, held, cuProjection(b.PG, where, want[1:]))
					run(fixture.Select("SELECT * FROM public."+table, []string{"keep"}, []string{"INT4"}, [][]any{{int64(9)}}))
					return
				}
				cuSeed(run)
				run(fixture.Exec("INSERT INTO cu_scope.t VALUES(7,8,9)", 1))
				s := cuProjection(b.PG, "table_schema='cu_scope'", cuSeedRows(database))
				held := cduHold(t, p, c, s.Query)
				check := func() {
					cuCheck(t, p, c, r, held, s)
					run(fixture.Select("SELECT * FROM cu_scope.t", []string{"a", "b", "plain"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(8), int64(9)}}))
				}
				check()
				assertFailure := func(s fixture.Step) { run(s); check() }
				cuMissing(assertFailure)
				cuReadOnly(assertFailure, []string{"INSERT", "UPDATE", "DELETE"})
				for _, q := range []string{"INSERT INTO information_schema.column_udt_usage(missing) VALUES('fake')", "UPDATE information_schema.column_udt_usage SET missing='fake' WHERE FALSE", "DELETE FROM information_schema.column_udt_usage WHERE missing='fake'"} {
					message := `column "missing" does not exist`
					if !strings.HasPrefix(q, "DELETE") {
						message = `column "missing" of relation "column_udt_usage" does not exist`
					}
					d := fixture.ControlsReferenceDiagnostic("42703", message)
					d.Position = strconv.Itoa(strings.Index(q, "missing") + 1)
					for _, prepared := range []bool{false, true} {
						failure := fixture.ErrorDiagnostic(q, d)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						if prepared {
							failure.ErrorStage = "prepare"
						}
						assertFailure(failure)
					}
				}
			})
		})
	}
}
