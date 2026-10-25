package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLineCallable(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"line_eq_reader"})
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, name := range []string{"line_eq", "pg_catalog.line_eq"} {
			q := "SELECT " + name + "($1::line,$2::line) AS eq"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				a, b any
				want any
			}{{"{1,2,3}", "{2,4,6}", true}, {"{1,2,3}", "{2,4,7}", false}, {nil, "{2,4,6}", nil}, {"{1,2,3}", nil, nil}, {[]byte(nil), nil, nil},
				{[]byte("{1,2,3}"), []byte("{2,4,6}"), true},
				{"{NaN,2,3}", "{NaN,2,3}", true}, {"{NaN,2,3}", "{NaN,4,6}", false},
			} {
				s := fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.a, tc.b)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type line: \"bad\"")
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic(q, d, "bad", "{2,4,6}")
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}, "{1,2,3}", "{2,4,6}"))
			stmt.Close()
		}
		for _, q := range []string{"SELECT $1::line=$2::line AS eq", "SELECT pg_catalog.line_eq($1::line,$2::line) AS eq"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ a, b, kind string }{{"{1e308,1,0}", "{0.00001,1,0}", "overflow"}, {"{1e-308,1,0}", "{1e308,1,0}", "underflow"}} {
				for _, prepared := range []bool{false, true} {
					run(boxError(q, "22003", "value out of range: "+tc.kind, "", "", prepared, true, tc.a, tc.b))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, boxError(q, "22003", "value out of range: "+tc.kind, "", "", false, true, tc.a, tc.b))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}, "{1,2,3}", "{2,4,6}"))
			}
		}
		for _, tc := range []struct{ q, signature string }{
			{"SELECT line_eq()", "line_eq()"},
			{"SELECT line_eq(line '{1,2,3}')", "line_eq(line)"},
			{"SELECT line_eq(line '{1,2,3}',line '{2,4,6}',line '{1,2,3}')", "line_eq(line, line, line)"},
			{"SELECT line_eq(1,line '{1,2,3}')", "line_eq(integer, line)"},
		} {
			for _, prepared := range []bool{false, true} {
				run(boxError(tc.q, "42883", "function "+tc.signature+" does not exist", "8", "No function matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
			}
		}
		run(fixture.Exec("CREATE ROLE line_eq_reader", 0))
		target := "pg_catalog.line_eq(line,line)"
		defer func() {
			for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + target + " FROM line_eq_reader"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO line_eq_reader", 0))
		run(fixture.Exec("SET ROLE line_eq_reader", 0))
		var retained []fixture.HeldStatement
		for _, q := range []string{"SELECT pg_catalog.line_eq($1::line,$2::line) AS eq", "SELECT $1::line=$2::line AS eq"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			retained = append(retained, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
		}
		run(fixture.Exec("RESET ROLE", 0))
		for phase, allowed := range []bool{true, false, true, false, true} {
			if allowed {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO line_eq_reader", 0))
			} else {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM line_eq_reader", 0))
			}
			run(fixture.Exec("SET ROLE line_eq_reader", 0))
			for _, q := range []string{"SELECT pg_catalog.line_eq(line '{1,2,3}',line '{2,4,6}') AS eq", "SELECT line '{1,2,3}'=line '{2,4,6}' AS eq"} {
				for _, prepared := range []bool{false, true} {
					if allowed {
						s := fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{true}})
						s.Prepared = prepared
						run(s)
					} else {
						run(boxError(q, "42501", "permission denied for function line_eq", "", "", prepared, true))
					}
				}
			}
			for provider, held := range retained {
				for input, tc := range []struct {
					a, b any
					want any
				}{{"{1,2,3}", "{2,4,6}", true}, {"{1,2,3}", "{2,4,7}", false}, {nil, "{2,4,6}", nil}, {"{1,2,3}", nil, nil}, {nil, nil, nil}} {
					s := fixture.Select(held.SQL, []string{"eq"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.a, tc.b)
					// The oracle folds strict NULL custom-plan inputs before EXECUTE
					// authority is demanded; nonnull calls still require the grant.
					if !allowed && tc.want != nil {
						s = boxError(held.SQL, "42501", "permission denied for function line_eq", "", "", false, true, tc.a, tc.b)
					}
					label := fmt.Sprintf("authority/phase%d/provider%d/input%d", phase, provider, input)
					t.Run(label+"/direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) })
					t.Run(label+"/new-prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s)) })
					heldStep := s
					// Observed PG generic-plan transition retains EXECUTE demand
					// for these NULL parameters. Native retains analysis identity,
					// not an executable plan cache, and folds strict NULL each run.
					if !allowed && (tc.want != nil || b.PG) {
						heldStep = boxError(held.SQL, "42501", "permission denied for function line_eq", "", "", false, true, tc.a, tc.b)
					}
					t.Run(label+"/held", func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, heldStep) })
				}
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
	})
}
