package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorLifetime(t *testing.T) {
	for _, route := range []string{"direct", "prepared", "held"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				plCursorHelpers(t, p, b, c)
				plCursorFunction(t, p, b, c, "pc_copy_take", "p refcursor", "integer", "DECLARE q refcursor; x integer; BEGIN q:=p; FETCH q INTO x; RETURN x; END")
				other, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_shared'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_shared"}}))
				q := "SELECT pc_take($1::refcursor) AS v"
				var exec fixture.Executor = c
				if route == "held" {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					exec = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				take := func(n any) {
					s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{n}}, "pc_shared")
					if route == "prepared" {
						s = fixture.Prepared(s)
					}
					sqlCursorStep(t, p, b, exec, s)
				}
				take(int64(10))
				take(int64(20))
				plCursorError(t, p, b, other, "SELECT pc_take('pc_shared'::refcursor) AS v", plCursorMissing("pc_shared"), true, false)
				s := fixture.Select("SELECT pc_copy_take('pc_shared'::refcursor) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}})
				if route != "direct" {
					s = fixture.Prepared(s)
				}
				sqlCursorStep(t, p, b, c, s)
				take(nil)
				sqlCursorStep(t, p, b, other, fixture.Exec("BEGIN", 0))
				sqlCursorStep(t, p, b, other, fixture.Select("SELECT pc_open('pc_shared'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_shared"}}))
				sqlCursorStep(t, p, b, other, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}, "pc_shared"))
				sqlCursorStep(t, p, b, other, fixture.Exec("ROLLBACK", 0))
				sqlCursorStep(t, p, b, c, fixture.Exec("SELECT pc_close('pc_shared'::refcursor)", 1))
				plCursorError(t, p, b, c, "SELECT pc_take('pc_shared'::refcursor) AS v", plCursorMissing("pc_shared"), true, true)
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_shared'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_shared"}}))
				take(int64(10))
				sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				plCursorError(t, p, b, c, "SELECT pc_take('pc_shared'::refcursor) AS v", plCursorMissing("pc_shared"), true, true)
			})
		})
	}
	for _, end := range []string{"COMMIT", "ROLLBACK", "autocommit", "physical_session"} {
		t.Run("end_"+end, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				plCursorHelpers(t, p, b, c)
				if end != "autocommit" {
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT pc_open('pc_ended'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_ended"}})
					if prepared {
						s = fixture.Prepared(s)
					}
					sqlCursorStep(t, p, b, c, s)
					if end != "autocommit" {
						break
					}
				}
				if end == "physical_session" {
					// ErrBadConn is the public database/sql contract for discarding
					// the physical owner rather than merely returning it to the pool.
					_ = c.Raw(func(any) error { return driver.ErrBadConn })
					fresh, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer fresh.Close()
					plCursorError(t, p, b, fresh, "SELECT pc_take('pc_ended'::refcursor) AS v", plCursorMissing("pc_ended"), true, false)
					return
				}
				if end != "autocommit" {
					sqlCursorStep(t, p, b, c, fixture.Exec(end, 0))
				}
				plCursorError(t, p, b, c, "SELECT pc_take('pc_ended'::refcursor) AS v", plCursorMissing("pc_ended"), true, false)
			})
		})
	}
	t.Run("savepoint", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorRows(t, p, b, c)
			plCursorHelpers(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_before'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_before"}}))
			sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pc_mark", 0))
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_after'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_after"}}))
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pc_mark", 0))
			plCursorError(t, p, b, c, "SELECT pc_take('pc_after'::refcursor) AS v", plCursorMissing("pc_after"), true, true)
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_take('pc_before'::refcursor) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		})
	})
	t.Run("generated_pair", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorRows(t, p, b, c)
			plCursorHelpers(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			names := make([]string, 2)
			q := "SELECT pc_open(NULL::refcursor) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i := range names {
				rows, e := stmt.QueryContext(p.Ctx)
				if e != nil {
					t.Fatal(e)
				}
				cols, e := rows.ColumnTypes()
				if e != nil {
					rows.Close()
					t.Fatal(e)
				}
				if len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != "REFCURSOR" {
					rows.Close()
					t.Fatal("generated reference metadata", cols)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing reference")
				}
				if e = rows.Scan(&names[i]); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("reference cardinality")
				}
				rows.Close()
			}
			if names[0] == "" || names[1] == "" || names[0] == names[1] {
				t.Fatalf("generated identities: %q", names)
			}
			for _, name := range names {
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_take($1::refcursor) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}, name))
			}
		})
	})
}
