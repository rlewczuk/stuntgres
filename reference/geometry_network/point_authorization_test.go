package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePointAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"point_reader"})
	fixture.SchemaReferenceCleanup(t, "point_acl")
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE point_reader", "CREATE SCHEMA point_acl", "CREATE TABLE point_acl.point_acl_values(id integer,p point,i integer,x float8)", "INSERT INTO point_acl.point_acl_values VALUES(1,'1,2',0,7)"} {
			count := int64(0)
			if q == "INSERT INTO point_acl.point_acl_values VALUES(1,'1,2',0,7)" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		defer func() {
			for _, q := range []string{
				"RESET ROLE",
				"GRANT EXECUTE ON FUNCTION pg_catalog.point_eq(point,point) TO PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.point_vert(point,point) TO PUBLIC",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.point_eq(point,point) FROM point_reader",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.point_vert(point,point) FROM point_reader",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("SET ROLE point_reader", 0))
		for _, prepared := range []bool{false, true} {
			run(boxError("SELECT p FROM point_acl.point_acl_values", "42501", "permission denied for schema point_acl", "15", "", prepared, false))
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA point_acl TO point_reader", 0))
		run(fixture.Exec("SET ROLE point_reader", 0))
		for _, q := range []string{"SELECT p FROM point_acl.point_acl_values", "UPDATE point_acl.point_acl_values SET p=point '3,4'"} {
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42501", "permission denied for table point_acl_values", "", "", prepared, true))
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT(p),UPDATE(p) ON point_acl.point_acl_values TO point_reader", 0))
		run(fixture.Exec("SET ROLE point_reader", 0))
		s := boxSelect(b, "SELECT p FROM point_acl.point_acl_values", []string{"p"}, []string{"POINT"}, [][]any{{"(1,2)"}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE SELECT(p) ON point_acl.point_acl_values FROM point_reader", 0))
		run(fixture.Exec("SET ROLE point_reader", 0))
		run(fixture.Exec("UPDATE point_acl.point_acl_values SET p[0]=5", 1))
		for _, q := range []string{"SELECT id FROM point_acl.point_acl_values", "UPDATE point_acl.point_acl_values SET p[i]=8", "UPDATE point_acl.point_acl_values SET p[1]=x"} {
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42501", "permission denied for table point_acl_values", "", "", prepared, true))
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT(p,i,x) ON point_acl.point_acl_values TO point_reader", 0))
		run(fixture.Exec("SET ROLE point_reader", 0))
		run(fixture.Exec("UPDATE point_acl.point_acl_values SET p[i]=x", 1))
		run(boxSelect(b, "SELECT p FROM point_acl.point_acl_values", []string{"p"}, []string{"POINT"}, [][]any{{"(7,2)"}}))
		run(fixture.Exec("RESET ROLE", 0))
		for _, tc := range []struct{ routine, op string }{{"point_eq", "~="}, {"point_vert", "?|"}} {
			target := "pg_catalog." + tc.routine + "(point,point)"
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO point_reader", 0))
			run(fixture.Exec("SET ROLE point_reader", 0))
			var held []fixture.HeldStatement
			for _, q := range []string{"SELECT $1::point " + tc.op + " $2::point AS v", "SELECT pg_catalog." + tc.routine + "($1::point,$2::point) AS v"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
			}
			run(fixture.Exec("RESET ROLE", 0))
			for _, allowed := range []bool{true, false, true} {
				verb := "REVOKE EXECUTE ON FUNCTION " + target + " FROM point_reader"
				if allowed {
					verb = "GRANT EXECUTE ON FUNCTION " + target + " TO point_reader"
				}
				run(fixture.Exec(verb, 0))
				run(fixture.Exec("SET ROLE point_reader", 0))
				for _, h := range held {
					s := fixture.Select(h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, "1,2", "1,2")
					if !allowed {
						s = boxError(h.SQL, "42501", "permission denied for function "+tc.routine, "", "", false, true, "1,2", "1,2")
					}
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				}
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
		}
	})
}
