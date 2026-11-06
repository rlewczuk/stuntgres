package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSegIntervalPrivileges(t *testing.T) {
	for i, name := range segIntervalRoutines {
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"seg_interval_actor"})
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE seg_interval_actor", "GRANT USAGE,CREATE ON SCHEMA public TO seg_interval_actor", "CREATE TABLE seg_interval_acl(id integer,a seg,b seg)", "GRANT SELECT ON seg_interval_acl TO seg_interval_actor"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO seg_interval_acl VALUES (1,'0 .. 1','1 .. 2'),(2,NULL,NULL)", 2))
				expression := func(a, z string, operator bool) string {
					if operator {
						return a + " " + segIntervalOperators[i] + " " + z
					}
					return name + "(" + a + "," + z + ")"
				}
				var held []fixture.HeldStatement
				for _, operator := range []bool{false, true} {
					q := "SELECT " + expression("a", "b", operator) + " AS v FROM seg_interval_acl WHERE id=$1::integer"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
				}
				wantValue := [8]bool{false, false, true, false, false, true, false, false}[i]
				run(fixture.Exec("SET ROLE seg_interval_actor", 0))
				for _, h := range held {
					fixture.RunStep(t, p.Ctx, h, b.Recorder, segIntervalSelect(b, h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{wantValue}}, false, 1))
				}
				run(fixture.Exec("RESET ROLE", 0))
				signature := name + "(seg,seg)"
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC,seg_interval_actor", 0))
				run(fixture.Exec("SET ROLE seg_interval_actor", 0))
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
				for _, h := range held {
					for _, id := range []int{1, 2} {
						denied := fixture.ErrorDiagnostic(h.SQL, want, id)
						denied.SelectRows = true
						segIntervalBoth(t, p, b, c, denied)
						fixture.RunStep(t, p.Ctx, h, b.Recorder, denied)
					}
				}
				for _, operator := range []bool{false, true} {
					for _, pair := range [][2]string{{"NULL::seg", "'1'::seg"}, {"'1'::seg", "NULL::seg"}, {"NULL::seg", "NULL::seg"}, {"NULL::seg", "a"}, {"a", "NULL::seg"}} {
						q := "SELECT " + expression(pair[0], pair[1], operator) + " AS v"
						if pair[0] == "a" || pair[1] == "a" {
							q += " FROM seg_interval_acl WHERE id=1"
						}
						segIntervalBoth(t, p, b, c, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, false))
					}
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO seg_interval_actor", 0))
				run(fixture.Exec("SET ROLE seg_interval_actor", 0))
				for _, h := range held {
					for _, tc := range []struct {
						id   int
						want any
					}{{2, nil}, {1, wantValue}} {
						fixture.RunStep(t, p.Ctx, h, b.Recorder, segIntervalSelect(b, h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, false, tc.id))
					}
				}
				run(fixture.Exec("RESET ROLE", 0))
				if i == 0 {
					run(fixture.Exec("REVOKE USAGE ON TYPE seg FROM PUBLIC,seg_interval_actor", 0))
					run(fixture.Exec("SET ROLE seg_interval_actor", 0))
					// Type USAGE controls declarations, not casts of existing values.
					run(segIntervalSelect(b, "SELECT '1'::seg && '1'::seg AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false))
					want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for type seg")
					queryIntError(t, p, b, c, "CREATE TABLE seg_interval_denied(v seg)", false, false, false, want)
					run(fixture.Exec("RESET ROLE", 0))
				}
			})
		})
	}
}
