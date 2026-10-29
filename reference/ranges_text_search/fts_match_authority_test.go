package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSMatchAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fts_match_reader"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE fts_match_reader", 0))
		for i, signature := range ftsMatchSignatures {
			d := []struct {
				oid, operator, commutator, restriction, join int64
				volatility                                   string
				cost                                         float64
			}{
				{3634, 3636, 3637, 3686, 3687, "i", 1}, {3635, 3637, 3636, 3686, 3687, "i", 1},
				{3760, 3762, 0, 5040, 5041, "s", 100}, {3761, 3763, 0, 5040, 5041, "s", 100},
			}[i]
			formals := signature.left + "," + signature.right
			routine := "pg_catalog." + signature.name + "(" + formals + ")"
			facts := fixture.Select("SELECT oid::bigint,proname,oid="+ftsTypesQuote(routine)+"::regprocedure::oid AS signature,prorettype::bigint,proisstrict,provolatile,proparallel,procost::double precision,proretset,prolang::bigint,prosrc FROM pg_catalog.pg_proc WHERE oid="+fmt.Sprint(d.oid),
				[]string{"oid", "proname", "signature", "prorettype", "proisstrict", "provolatile", "proparallel", "procost", "proretset", "prolang", "prosrc"},
				[]string{"INT8", "NAME", "BOOL", "INT8", "BOOL", "CHAR", "CHAR", "FLOAT8", "BOOL", "INT8", "TEXT"},
				[][]any{{d.oid, signature.name, true, int64(16), true, d.volatility, "s", d.cost, false, int64(12), signature.name}})
			run(facts)
			run(fixture.Prepared(facts))
			operator := fixture.Select("SELECT oprname,oprleft::bigint,oprright::bigint,oprcode::oid::bigint AS routine,oprresult::bigint,oprkind,oprcom::bigint,oprnegate::bigint,oprrest::oid::bigint,oprjoin::oid::bigint,oprcanmerge,oprcanhash FROM pg_catalog.pg_operator WHERE oid="+fmt.Sprint(d.operator),
				[]string{"oprname", "oprleft", "oprright", "routine", "oprresult", "oprkind", "oprcom", "oprnegate", "oprrest", "oprjoin", "oprcanmerge", "oprcanhash"},
				[]string{"NAME", "INT8", "INT8", "INT8", "INT8", "CHAR", "INT8", "INT8", "INT8", "INT8", "BOOL", "BOOL"},
				[][]any{{"@@", ftsMatchTypeOID(signature.left), ftsMatchTypeOID(signature.right), d.oid, int64(16), "b", d.commutator, int64(0), d.restriction, d.join, false, false}})
			run(operator)
			run(fixture.Prepared(operator))
			for _, expression := range []string{
				signature.name + "($1::" + signature.left + ",$2::" + signature.right + ")",
				signature.expression(true), "$1::" + signature.left + " OPERATOR(pg_catalog.@@) $2::" + signature.right,
			} {
				query := "SELECT " + expression + " AS matched"
				s := ftsMatchStep(query, "matched", [][]any{{true}}, "cat", "cat")
				run(s)
				run(fixture.Prepared(s))
				func() {
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+routine+" FROM PUBLIC", 0))
					defer func() {
						for _, ddl := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + routine + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + routine + " FROM fts_match_reader"} {
							if _, err := c.ExecContext(p.Ctx, ddl); err != nil {
								t.Error(err)
							}
						}
					}()
					run(fixture.Exec("SET ROLE fts_match_reader", 0))
					denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+signature.name), "cat", "cat")
					denied.SelectRows = true
					run(denied)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
					run(fixture.Exec("RESET ROLE", 0))
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+routine+" TO fts_match_reader", 0))
					run(fixture.Exec("SET ROLE fts_match_reader", 0))
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}()
			}
		}
		run(fixture.Exec("CREATE OPERATOR public.## (LEFTARG=tsvector,RIGHTARG=tsquery,PROCEDURE=pg_catalog.ts_match_vq)", 0))
		custom := ftsMatchStep("SELECT $1::tsvector OPERATOR(public.##) $2::tsquery", "?column?", [][]any{{true}}, "cat", "cat")
		run(custom)
		run(fixture.Prepared(custom))
	})
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, signature := range ftsMatchSignatures {
			for _, decoration := range []struct{ suffix, message string }{
				{" FILTER (WHERE true)", "FILTER specified, but " + signature.name + " is not an aggregate function"},
				{" OVER ()", "OVER specified, but " + signature.name + " is not a window function nor an aggregate function"},
			} {
				d := fixture.ControlsReferenceDiagnostic("42809", decoration.message)
				d.Position = "8"
				ftsOpsDiagnosticRoutes(t, p, b, "SELECT "+signature.name+"('cat'::"+signature.left+",'cat'::"+signature.right+")"+decoration.suffix, d, true)
			}
			q := "SELECT public." + signature.name + "('cat'::" + signature.left + ",'cat'::" + signature.right + ")"
			d := fixture.ControlsReferenceDiagnostic("42883", "function public."+signature.name+"("+signature.left+", "+signature.right+") does not exist")
			d.Position = "8"
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			ftsOpsDiagnosticRoutes(t, p, b, q, d, true)
		}
	})
}

func ftsMatchTypeOID(name string) int64 {
	switch name {
	case "tsvector":
		return 3614
	case "tsquery":
		return 3615
	default:
		return 25
	}
}
