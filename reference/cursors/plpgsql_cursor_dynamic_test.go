package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorDynamic(t *testing.T) {
	t.Run("values", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorFunction(t, p, b, c, "pc_dynamic", "a integer", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR EXECUTE 'SELECT $1::integer+$2::integer AS i' USING 7,8; FETCH c INTO x; CLOSE c; RETURN x; END")
			plCursorValue(t, p, b, c, "SELECT pc_dynamic($1::integer) AS v", "INT4", int64(15), 100)
			plCursorFunction(t, p, b, c, "pc_null", "", "integer", "DECLARE c refcursor; x integer:=9; BEGIN OPEN c FOR EXECUTE 'SELECT $1::integer AS i' USING NULL::integer; FETCH c INTO x; CLOSE c; RETURN x; END")
			plCursorValue(t, p, b, c, "SELECT pc_null() AS v", "INT4", nil)
			plCursorFunction(t, p, b, c, "pc_command", "q text", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR EXECUTE q; FETCH c INTO x; CLOSE c; RETURN x; END")
			q := "SELECT pc_command($1::text) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, n := range []int64{7, 8, 7} {
				command := "SELECT 7 AS i"
				if n == 8 {
					command = "SELECT 8 AS i"
				}
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{n}}, command)
				sqlCursorStep(t, p, b, c, s)
				sqlCursorStep(t, p, b, c, fixture.Prepared(s))
				sqlCursorStep(t, p, b, held, s)
			}
		})
	})
	t.Run("using_once_written_order", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_seq", 0))
			plCursorFunction(t, p, b, c, "pc_order", "", "bigint", "DECLARE c refcursor; x bigint; BEGIN OPEN c FOR EXECUTE 'SELECT $1::bigint*100+$2::bigint' USING nextval('pc_seq'),nextval('pc_seq'); FETCH c INTO x; CLOSE c; RETURN x; END")
			q := "SELECT pc_order() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			sqlCursorStep(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(102)}}))
			sqlCursorStep(t, p, b, c, fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(304)}})))
			for _, n := range []int64{506, 708} {
				sqlCursorStep(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{n}}))
			}
			plCursorValue(t, p, b, c, "SELECT currval('pc_seq') AS v", "INT8", int64(8))
		})
	})
}
