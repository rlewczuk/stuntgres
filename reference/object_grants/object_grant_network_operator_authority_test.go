package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Declaration facts are REL_18_0 pg_proc.dat 2627–2633 and
// system_functions.sql's RETURN $2 + $1 for 2631. ACL/context and warmed-plan
// expectations below are independent hypotheses until the root's pinned PG18
// run; released source is not a passing oracle observation.
func TestReferenceObjectGrantNetworkOperatorAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, signature, inputs, result, row, qualified, constant, parameter, cidr, literalNull, runtimeNull, sibling string
		oid, arity, resultOID                                                                                         int64
		want, cidrWant, siblingWant                                                                                   any
		bad, badCode, badMessage                                                                                      string
	}{
		{"inetnot", "inet", "869", "INET", "~ip", "OPERATOR(pg_catalog.~) ip", "~INET '192.168.1.6/24'", "~(CASE WHEN id=1 THEN $1::INET ELSE ip END)", "~network", "~NULL::INET", "~ni", "ip & mask", 2627, 1, 869, "63.87.254.249/24", "63.87.254.255/24", "0.0.0.6/25", "", "", ""},
		{"inetand", "inet,inet", "869 869", "INET", "ip & mask", "ip OPERATOR(pg_catalog.&) mask", "INET '192.168.1.6/24' & INET '0.0.0.255/25'", "(CASE WHEN id=1 THEN $1::INET ELSE ip END) & mask", "network & mask", "NULL::INET & INET '::1'", "ni & mask", "ip | mask", 2628, 2, 869, "0.0.0.6/25", "0.0.0.0/25", "192.168.1.255/25", "ip & INET '::1'", "22023", "cannot AND inet values of different sizes"},
		{"inetor", "inet,inet", "869 869", "INET", "ip | mask", "ip OPERATOR(pg_catalog.|) mask", "INET '192.168.1.6/24' | INET '0.0.0.255/25'", "(CASE WHEN id=1 THEN $1::INET ELSE ip END) | mask", "network | mask", "NULL::INET | INET '::1'", "ni | mask", "ip & mask", 2629, 2, 869, "192.168.1.255/25", "192.168.1.255/25", "0.0.0.6/25", "ip | INET '::1'", "22023", "cannot OR inet values of different sizes"},
		{"inetpl", "inet,bigint", "869 20", "INET", "ip + n", "ip OPERATOR(pg_catalog.+) n", "INET '192.168.1.6/24' + 1::BIGINT", "(CASE WHEN id=1 THEN $1::INET ELSE ip END) + n", "network + n", "NULL::INET + 1::BIGINT", "ni + n", "ip - n", 2630, 2, 869, "192.168.1.7/24", "192.168.1.1/24", "192.168.1.5/24", "INET '255.255.255.255' + n", "22003", "result is out of range"},
		{"int8pl_inet", "bigint,inet", "20 869", "INET", "n + ip", "n OPERATOR(pg_catalog.+) ip", "1::BIGINT + INET '192.168.1.6/24'", "n + (CASE WHEN id=1 THEN $1::INET ELSE ip END)", "n + network", "1::BIGINT + NULL::INET", "n + ni", "ip - n", 2631, 2, 869, "192.168.1.7/24", "192.168.1.1/24", "192.168.1.5/24", "n + INET '255.255.255.255'", "22003", "result is out of range"},
		{"inetmi_int8", "inet,bigint", "869 20", "INET", "ip - n", "ip OPERATOR(pg_catalog.-) n", "INET '192.168.1.6/24' - 1::BIGINT", "(CASE WHEN id=1 THEN $1::INET ELSE ip END) - n", "network - n", "NULL::INET - 1::BIGINT", "ni - n", "ip + n", 2632, 2, 869, "192.168.1.5/24", "192.168.0.255/24", "192.168.1.7/24", "INET '0.0.0.0' - n", "22003", "result is out of range"},
		{"inetmi", "inet,inet", "869 869", "INT8", "ip - peer", "ip OPERATOR(pg_catalog.-) peer", "INET '192.168.1.6/24' - INET '192.168.1.5'", "(CASE WHEN id=1 THEN $1::INET ELSE ip END) - peer", "network - peer", "NULL::INET - INET '::1'", "ni - peer", "ip + n", 2633, 2, 20, int64(1), int64(-5), "192.168.1.7/24", "ip - INET '::1'", "22023", "cannot subtract inet values of different sizes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_net_authority"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + tc.name + "(" + tc.signature + ")"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Select("SELECT oid::regprocedure::TEXT AS identity,proname,pronamespace::BIGINT AS namespace,pronargs,prorettype::BIGINT AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1",
					[]string{"identity", "proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"},
					[]string{"TEXT", "NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"},
					[][]any{{tc.name + "(" + tc.signature + ")", tc.name, int64(11), tc.arity, tc.resultOID, true, "i", "s"}}, tc.oid))
				// Native intentionally projects only its existing nine columns.
				// Query the complete released declaration independently on PG, and
				// assert the same query's existing native capability boundary.
				full := "SELECT p.proargtypes::TEXT AS inputs,p.prokind,p.prolang::BIGINT AS language,p.proretset,p.pronargdefaults,p.provariadic::BIGINT AS variadic,p.proargnames IS NULL AS unnamed FROM pg_proc p WHERE p.oid=$1"
				language := int64(12)
				if tc.oid == 2631 {
					language = 14
				}
				metadata := fixture.Select(full, []string{"inputs", "prokind", "language", "proretset", "pronargdefaults", "variadic", "unnamed"}, []string{"TEXT", "CHAR", "INT8", "BOOL", "INT2", "INT8", "BOOL"}, [][]any{{tc.inputs, "f", language, false, int64(0), int64(0), true}}, tc.oid)
				if !b.PG {
					metadata = fixture.ImplicitError(full, fixture.Diagnostic{Code: "42703", Message: "column p.proargtypes does not exist", Position: fixture.Position(strings.Index(full, "p.proargtypes") + 1)}, tc.oid)
					metadata.SelectRows, metadata.ErrorStage = true, "execute"
					if prepared {
						metadata.ErrorStage = "prepare"
					}
				}
				run(metadata)
				run(fixture.Exec("CREATE SCHEMA og_net_authority AUTHORIZATION og_object_other", 0))
				fixture.RoleGrantActor(run, "og_object_other", func() {
					run(fixture.Exec("CREATE TABLE og_net_authority.source(id INTEGER,ip INET,mask INET,peer INET,network CIDR,n BIGINT,ni INET,nn BIGINT)", 0))
					run(fixture.Exec("INSERT INTO og_net_authority.source VALUES(1,'192.168.1.6/24','0.0.0.255/25','192.168.1.5','192.168.1/24',1,NULL,NULL)", 1))
					run(fixture.Exec("CREATE VIEW og_net_authority.saved AS SELECT "+tc.row+" AS v FROM og_net_authority.source", 0))
					run(fixture.Exec("GRANT SELECT ON og_net_authority.source,og_net_authority.saved TO og_object_reader", 0))
				})
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT ip FROM og_net_authority.source", "schema", "og_net_authority", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_net_authority TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_net_authority.source"+tail, []string{"v"}, []string{tc.result}, [][]any{{want}}, args...)
				}
				selectOnly := func(expr string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{tc.result}, [][]any{{want}}, args...)
				}
				// Row inputs defeat immutable constant folding. A parameter under
				// row-dependent CASE is not a bare constant-only held-plan witness.
				cases := []objectGrantProviderCase{
					{value(tc.row, "", tc.want), true, true},
					{value(tc.qualified, "", tc.want), true, true},
					{value(tc.parameter, "", tc.want, "192.168.1.6/24"), true, true},
					{value(tc.cidr, "", tc.cidrWant), true, true},
					{fixture.Select("SELECT v FROM og_net_authority.saved", []string{"v"}, []string{tc.result}, [][]any{{tc.want}}), true, true},
					{value(tc.runtimeNull, "", nil), true, true},
					// Fresh constants MUST check the selected declaration before
					// network folding, not bypass admission by calling Eval directly.
					{selectOnly(tc.constant, tc.want), true, false},
					{selectOnly(tc.literalNull, nil), false, false},
					{value("CASE WHEN FALSE THEN "+tc.row+" ELSE NULL END", "", nil), false, true},
				}
				freshNull := strings.ReplaceAll(tc.parameter, "CASE WHEN id=1 THEN $1::INET ELSE ip END", "$1::INET")
				cases = append(cases, objectGrantProviderCase{value(freshNull, "", nil, nil), false, false})
				if tc.arity == 2 {
					rightNull := "ip & ni"
					switch tc.name {
					case "inetor":
						rightNull = "ip | ni"
					case "inetpl":
						rightNull = "ip + nn"
					case "int8pl_inet":
						rightNull = "nn + ip"
					case "inetmi_int8":
						rightNull = "ip - nn"
					case "inetmi":
						rightNull = "ip - ni"
					}
					cases = append(cases, objectGrantProviderCase{value(rightNull, "", nil), true, true})
				}
				for _, tail := range []string{" WHERE FALSE", " WHERE id>99", " LIMIT 0"} {
					s := fixture.Select("SELECT "+tc.row+" AS v FROM og_net_authority.source"+tail, []string{"v"}, []string{tc.result}, nil)
					cases = append(cases, objectGrantProviderCase{s, true, true})
				}
				if tc.bad != "" {
					context := ""
					if tc.oid == 2631 && !b.PG {
						// Native executes its genuine commuted provider; PG inlines
						// the row expression and therefore has no wrapper context.
						context = `SQL function "int8pl_inet" statement 1`
					}
					s := fixture.ImplicitError("SELECT "+tc.bad+" AS v FROM og_net_authority.source", fixture.Diagnostic{Code: tc.badCode, Message: tc.badMessage, Context: context})
					s.SelectRows, s.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{s, true, false})
					// Also exercise the constant-folding error frontier: selected X
					// denial must precede the authorized semantic body error.
					constantBad := strings.ReplaceAll(tc.bad, "ip", "INET '192.168.1.6/24'")
					constantBad = strings.ReplaceAll(constantBad, " + n", " + 1::BIGINT")
					constantBad = strings.ReplaceAll(constantBad, " - n", " - 1::BIGINT")
					constantBad = strings.ReplaceAll(constantBad, "n + ", "1::BIGINT + ")
					context = ""
					if tc.oid == 2631 {
						context = `SQL function "int8pl_inet" statement 1`
					}
					s = fixture.ImplicitError("SELECT "+constantBad+" AS v", fixture.Diagnostic{Code: tc.badCode, Message: tc.badMessage, Context: context})
					s.SelectRows, s.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{s, true, false})
				}
				controls := []fixture.Step{
					fixture.Select("SELECT "+tc.sibling+" AS v FROM og_net_authority.source", []string{"v"}, []string{"INET"}, [][]any{{tc.siblingWant}}),
					fixture.Select("SELECT 6::BIGINT + 1::BIGINT AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(7)}}),
					fixture.Select("SELECT ip FROM og_net_authority.source", []string{"ip"}, []string{"INET"}, [][]any{{"192.168.1.6/24"}}),
				}
				// SQL wrapper inlining can erase the outer identity from a warmed
				// PG plan. Retain all actual held routes, but do not impose native
				// current-outer semantics on such PG parameter-free plans.
				var wrapperHeld []objectGrantProviderCase
				if tc.oid == 2631 {
					for i := range cases {
						if cases[i].held {
							wrapperHeld = append(wrapperHeld, cases[i])
							cases[i].held = false
						}
					}
				}
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", tc.name, cases, controls)
				fullDiagnostic := func(s fixture.Step, name, context string) fixture.Step {
					d := fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name, Context: context}, s.Args...)
					d.SelectRows, d.ErrorStage = true, "execute"
					return d
				}
				if len(wrapperHeld) != 0 {
					held := make([]fixture.HeldStatement, len(wrapperHeld))
					defer func() {
						for _, h := range held {
							if h.Statement != nil {
								h.Statement.Close()
							}
						}
					}()
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for i, wc := range wrapperHeld {
							stmt, err := c.PrepareContext(p.Ctx, wc.step.Query)
							if err != nil {
								t.Fatal(err)
							}
							held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: wc.step.Query}
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, wc.step)
						}
					})
					for phase, allowed := range []bool{false, true, false} {
						if phase == 0 {
							run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
						} else if allowed {
							run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
						} else {
							run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
						}
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							for i, wc := range wrapperHeld {
								fresh, warm := wc.step, wc.step
								if !allowed && wc.needed {
									fresh = fullDiagnostic(wc.step, tc.name, "")
									if !b.PG || len(wc.step.Args) != 0 {
										warm = fresh
									}
								}
								run(fresh)
								fixture.RunStep(t, p.Ctx, held[i], b.Recorder, warm)
							}
						})
					}
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
				}
				// Table SELECT is independent of routine X. A normal view reads
				// its owner's table rights, but evaluates routines as current invoker.
				run(fixture.Exec("REVOKE SELECT ON og_net_authority.source FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT ip FROM og_net_authority.source", "table", "source")
					run(fixture.Select("SELECT v FROM og_net_authority.saved", []string{"v"}, []string{tc.result}, [][]any{{tc.want}}))
				})
				run(fixture.Exec("GRANT SELECT ON og_net_authority.source TO og_object_reader", 0))
				// A demanded constant child error is not excused by an unrelated
				// strict NULL result; this is not a blanket NULL-masks-errors rule.
				s := fixture.ImplicitError("SELECT "+tc.literalNull+" AS v,1/0 AS boom", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(s) })
			})
		})
	}

	t.Run("sql_wrapper_inner_current_execute", func(t *testing.T) {
		objectGrantScenario(t, []string{"og_net_body"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			outer, inner := "pg_catalog.int8pl_inet(bigint,inet)", "pg_catalog.inetpl(inet,bigint)"
			defer objectGrantRoutineCleanup(t, p, c, []string{outer, inner})
			run(fixture.Exec("CREATE SCHEMA og_net_body AUTHORIZATION og_object_other", 0))
			fixture.RoleGrantActor(run, "og_object_other", func() {
				run(fixture.Exec("CREATE TABLE og_net_body.source(ip INET,n BIGINT)", 0))
				run(fixture.Exec("INSERT INTO og_net_body.source VALUES('192.168.1.6/24',1)", 1))
				run(fixture.Exec("CREATE VIEW og_net_body.saved AS SELECT n + ip AS v FROM og_net_body.source", 0))
				run(fixture.Exec("GRANT SELECT ON og_net_body.source,og_net_body.saved TO og_object_reader", 0))
			})
			run(fixture.Exec("GRANT USAGE ON SCHEMA og_net_body TO og_object_reader", 0))
			cases := []fixture.Step{
				fixture.Select("SELECT n + ip AS v FROM og_net_body.source", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.7/24"}}),
				fixture.Select("SELECT n + CASE WHEN n=1 THEN $1::INET ELSE ip END AS v FROM og_net_body.source", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.7/24"}}, "192.168.1.6/24"),
				fixture.Select("SELECT v FROM og_net_body.saved", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.7/24"}}),
				fixture.Select("SELECT 1::BIGINT + INET '192.168.1.6/24' AS v", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.7/24"}}),
				fixture.Select("SELECT $1::BIGINT + $2::INET AS v", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.7/24"}}, int64(1), "192.168.1.6/24"),
			}
			held := make([]fixture.HeldStatement, 3)
			defer func() {
				for _, h := range held {
					if h.Statement != nil {
						h.Statement.Close()
					}
				}
			}()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for i := range held {
					stmt, err := c.PrepareContext(p.Ctx, cases[i].Query)
					if err != nil {
						t.Fatal(err)
					}
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: cases[i].Query}
					fixture.RunStep(t, p.Ctx, held[i], b.Recorder, cases[i])
				}
			})
			for phase, allowed := range []bool{false, true, false} {
				if phase == 0 {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+inner+" FROM PUBLIC", 0))
				} else if allowed {
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+inner+" TO og_object_reader", 0))
				} else {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+inner+" FROM og_object_reader", 0))
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for i, good := range cases {
						s := good
						if !allowed {
							// PG row plans inline the real child; literal/custom constant
							// evaluation enters the SQL function. Pin these separately.
							context := ""
							if i >= 3 {
								context = `SQL function "int8pl_inet" statement 1`
							}
							s = fixture.ImplicitError(good.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function inetpl", Context: context}, good.Args...)
							s.SelectRows, s.ErrorStage = true, "execute"
						}
						run(s)
						if i < len(held) {
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						}
					}
					for _, q := range []string{"SELECT 1::BIGINT + NULL::INET AS v", "SELECT $1::BIGINT + $2::INET AS v"} {
						var args []any
						if strings.Contains(q, "$1") {
							args = []any{int64(1), nil}
						}
						run(fixture.Select(q, []string{"v"}, []string{"INET"}, [][]any{{nil}}, args...))
					}
					// No reciprocal edge, or shared arithmetic-token ACL: subtraction
					// remains callable while the actual addition child is denied.
					run(fixture.Select("SELECT ip - n AS v FROM og_net_body.source", []string{"v"}, []string{"INET"}, [][]any{{"192.168.1.5/24"}}))
				})
			}
			// Denying both must report the selected outer before entering its body.
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+outer+" FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				s := fixture.ImplicitError(cases[0].Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function int8pl_inet"})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
			})
		})
	})
}
