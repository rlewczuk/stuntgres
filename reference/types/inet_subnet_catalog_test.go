package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETSubnetCatalogPaths(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "subnet_shadow")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE subnet_paths(a INET,b INET)", 0))
		run(fixture.Exec("INSERT INTO subnet_paths VALUES('192.168.1.255/25','192.168.1.199/24')", 1))
		for i, tc := range []struct {
			name, alias        string
			oid, routine, comm int64
			want               bool
		}{
			{"network_sub", "#<#", 931, 927, 933, true}, {"network_subeq", "#<=#", 932, 928, 934, true},
			{"network_sup", "#>#", 933, 929, 931, false}, {"network_supeq", "#>=#", 934, 930, 932, false},
			{"network_overlap", "#&#", 3552, 3551, 3552, true},
		} {
			q := fmt.Sprintf("SELECT oid::bigint AS oid,oprleft::bigint AS l,oprright::bigint AS r,oprresult::bigint AS result,oprcode::oid::bigint AS routine,oprcom::bigint AS comm FROM pg_operator WHERE oid=%d", tc.oid)
			s := fixture.Select(q, []string{"oid", "l", "r", "result", "routine", "comm"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{tc.oid, int64(869), int64(869), int64(16), tc.routine, tc.comm}})
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("CREATE OPERATOR public."+tc.alias+" (LEFTARG=inet,RIGHTARG=inet,FUNCTION=pg_catalog."+tc.name+")", 0))
			for _, e := range []string{"a " + inetSubnetOps[i] + " b", "a OPERATOR(pg_catalog." + inetSubnetOps[i] + ") b", "pg_catalog." + tc.name + "(a,b)", "a OPERATOR(public." + tc.alias + ") b"} {
				s := fixture.Select("SELECT "+e+" AS v FROM subnet_paths", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Exec("CREATE VIEW subnet_alias_view AS SELECT a OPERATOR(public.#<#) b AS v FROM subnet_paths", 0))
		q := "SELECT v FROM subnet_alias_view"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop operator #<#(inet,inet) because other objects depend on it")
		d.Detail = "view subnet_alias_view depends on operator #<#(inet,inet)"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP OPERATOR public.#<# (inet,inet)", d))
		run(fixture.Exec("DROP OPERATOR public.#<# (inet,inet) CASCADE", 0))
		d = fixture.ControlsReferenceDiagnostic("42P01", `relation "subnet_alias_view" does not exist`)
		d.Position = "15"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d))
		run(fixture.Exec("CREATE SCHEMA subnet_shadow", 0))
		run(fixture.Exec("CREATE OPERATOR subnet_shadow.<< (LEFTARG=inet,RIGHTARG=inet,FUNCTION=pg_catalog.network_sup)", 0))
		run(fixture.Exec("SET search_path=subnet_shadow,pg_catalog,public", 0))
		inetOperatorScalar(t, p, b, "INET '::1' << INET '::/0'", "BOOL", false)
		inetOperatorScalar(t, p, b, "INET '::1' OPERATOR(pg_catalog.<<) INET '::/0'", "BOOL", true)
		run(fixture.Exec("RESET search_path", 0))
		run(fixture.Exec("CREATE OPERATOR public.#?# (LEFTARG=inet,RIGHTARG=inet,FUNCTION=pg_catalog.network_sub,COMMUTATOR=OPERATOR(public.#!#))", 0))
		for _, tc := range []struct{ q, code, message, hint, marker string }{
			{"SELECT INET '::1' OPERATOR(subnet_absent.<<) INET '::/0'", "3F000", `schema "subnet_absent" does not exist`, "", "OPERATOR"},
			{"SELECT INET '::1' OPERATOR(public.#!#) INET '::/0'", "42883", "operator is only a shell: inet public.#!# inet", "", "OPERATOR"},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Hint = tc.hint
			d.Position = fixture.Position(strings.Index(tc.q, tc.marker) + 1)
			inetSubnetAnalysisError(t, p, b, tc.q, d)
		}
	}
}
