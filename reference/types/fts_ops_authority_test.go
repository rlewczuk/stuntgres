package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSOpsRoutineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fts_ops_reader"})
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		declarations := []struct {
			oid                            int
			name                           string
			result                         int
			signature, call, operator, typ string
			want                           any
			operatorOID                    int
		}{
			{3625, "tsvector_concat", 3614, "tsvector,tsvector", "pg_catalog.tsvector_concat('a:1','a:2B')", "'a:1'::tsvector OPERATOR(pg_catalog.||) 'a:2B'::tsvector", "TSVECTOR", "'a':1,3B", 3633},
			{3669, "tsquery_and", 3615, "tsquery,tsquery", "pg_catalog.tsquery_and('a','b')", "'a'::tsquery OPERATOR(pg_catalog.&&) 'b'::tsquery", "TSQUERY", "'a' & 'b'", 3680},
			{3670, "tsquery_or", 3615, "tsquery,tsquery", "pg_catalog.tsquery_or('a','b')", "'a'::tsquery OPERATOR(pg_catalog.||) 'b'::tsquery", "TSQUERY", "'a' | 'b'", 3681},
			{3671, "tsquery_not", 3615, "tsquery", "pg_catalog.tsquery_not('a')", "OPERATOR(pg_catalog.!!) 'a'::tsquery", "TSQUERY", "!'a'", 3682},
			{5003, "tsquery_phrase", 3615, "tsquery,tsquery", "pg_catalog.tsquery_phrase('a','b')", "'a'::tsquery OPERATOR(pg_catalog.<->) 'b'::tsquery", "TSQUERY", "'a' <-> 'b'", 5005},
			{5004, "tsquery_phrase", 3615, "tsquery,tsquery,integer", "pg_catalog.tsquery_phrase('a','b',2)", "", "TSQUERY", "'a' <2> 'b'", 0},
			{3691, "tsq_mcontains", 16, "tsquery,tsquery", "pg_catalog.tsq_mcontains('a | b','a')", "'a | b'::tsquery OPERATOR(pg_catalog.@>) 'a'::tsquery", "BOOL", true, 3693},
			{3692, "tsq_mcontained", 16, "tsquery,tsquery", "pg_catalog.tsq_mcontained('a','a | b')", "'a'::tsquery OPERATOR(pg_catalog.<@) 'a | b'::tsquery", "BOOL", true, 3694},
		}
		run(fixture.Exec("CREATE ROLE fts_ops_reader", 0))
		for _, d := range declarations {
			t.Run(fmt.Sprint(d.oid), func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				// Resolve exact formals through regprocedure; pg_proc's bounded native
				// projection intentionally does not expose proargtypes.
				signature := "pg_catalog." + d.name + "(" + d.signature + ")"
				run(fixture.Select("SELECT oid::bigint AS oid,proname,oid="+ftsTypesQuote(signature)+"::regprocedure::oid AS signature,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_catalog.pg_proc WHERE oid="+fmt.Sprint(d.oid), []string{"oid", "proname", "signature", "result", "proisstrict", "provolatile", "proparallel"}, []string{"INT8", "NAME", "BOOL", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(d.oid), d.name, true, int64(d.result), true, "i", "s"}}))
				ftsOpsValue(t, p, b, d.call, d.typ, d.want)
				ftsOpsValue(t, p, b, d.call[len("pg_catalog."):], d.typ, d.want)
				expressions := []string{d.call}
				if d.operator != "" {
					ftsOpsValue(t, p, b, d.operator, d.typ, d.want)
					expressions = append(expressions, d.operator)
					kind := "b"
					if d.operatorOID == 3682 {
						kind = "l"
					}
					var commutator, restriction, join int64
					switch d.operatorOID {
					case 3693:
						commutator, restriction, join = 3694, 5040, 5041
					case 3694:
						commutator, restriction, join = 3693, 5040, 5041
					}
					run(fixture.Select("SELECT oprcode::oid::bigint AS routine,oprresult::bigint AS result,oprkind,oprcom::bigint AS commutator,oprnegate::bigint AS negator,oprrest::oid::bigint AS restriction,oprjoin::oid::bigint AS join_estimator,oprcanmerge,oprcanhash FROM pg_catalog.pg_operator WHERE oid="+fmt.Sprint(d.operatorOID), []string{"routine", "result", "oprkind", "commutator", "negator", "restriction", "join_estimator", "oprcanmerge", "oprcanhash"}, []string{"INT8", "INT8", "CHAR", "INT8", "INT8", "INT8", "INT8", "BOOL", "BOOL"}, [][]any{{int64(d.oid), int64(d.result), kind, commutator, int64(0), restriction, join, false, false}}))
				}
				// Held statements must recheck actual routine admission after ACL changes.
				for _, expression := range expressions {
					func() {
						query := "SELECT " + expression + " AS result"
						stmt, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
						signature := "pg_catalog." + d.name + "(" + d.signature + ")"
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
						defer func() {
							if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
								t.Error(err)
							}
							if _, err := conn.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
								t.Error(err)
							}
							if _, err := conn.ExecContext(p.Ctx, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM fts_ops_reader"); err != nil {
								t.Error(err)
							}
						}()
						run(fixture.Exec("SET ROLE fts_ops_reader", 0))
						denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+d.name))
						denied.SelectRows = true
						fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
						run(denied)
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO fts_ops_reader", 0))
						run(fixture.Exec("SET ROLE fts_ops_reader", 0))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{d.typ}, [][]any{{d.want}}))
						run(fixture.Exec("RESET ROLE", 0))
					}()
				}
			})
		}
		// Shared spellings must retain non-FTS overloads, independently of FTS values.
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{"'a'::text || 'b'::text", "TEXT", "ab"},
			{"ARRAY[1,2] && ARRAY[2,3]", "BOOL", true},
			{"ARRAY[1,2] @> ARRAY[2]", "BOOL", true},
			{"ARRAY[2] <@ ARRAY[1,2]", "BOOL", true},
			{"'{\"a\":1}'::jsonb @> '{\"a\":1}'::jsonb", "BOOL", true},
			{"'{\"a\":1}'::jsonb || '{\"b\":2}'::jsonb", "JSONB", "{\"a\": 1, \"b\": 2}"},
			{"'0,0,1'::circle <-> '5,0,1'::circle", "FLOAT8", float64(3)},
			{"'0,0,1'::circle && '2,0,1'::circle", "BOOL", true},
			{"inet '10.0.0.0/24' && inet '10.0.0.1'", "BOOL", true},
		} {
			ftsOpsValue(t, p, b, tc.expr, tc.typ, tc.want)
		}
		run(fixture.Exec("CREATE TABLE fts_ops_overloads(a int4range,b int4range)", 0))
		run(fixture.Exec("INSERT INTO fts_ops_overloads VALUES('[1,4)','[3,5)')", 1))
		for _, expression := range []string{"a && b", "a @> 2"} {
			s := fixture.Select("SELECT "+expression+" AS result FROM fts_ops_overloads", []string{"result"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
