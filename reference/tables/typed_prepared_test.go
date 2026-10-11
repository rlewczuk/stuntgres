package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTablePreparedLifecycle(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		ddl, err := c.PrepareContext(p.Ctx, "CREATE TABLE t OF pair")
		if err != nil {
			t.Fatal(err)
		}
		defer ddl.Close()
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='t'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		result, err := ddl.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		insert, err := c.PrepareContext(p.Ctx, "INSERT INTO t(id,note) VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		star, err := c.PrepareContext(p.Ctx, "SELECT * FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer star.Close()
		fixed, err := c.PrepareContext(p.Ctx, "SELECT id FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer fixed.Close()
		result, err = insert.ExecContext(p.Ctx, int64(11), "hey")
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		rows, err := star.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Error("changed cached result exposed cursor")
		}
		d := typedTableError("0A000", "cached plan must not change result type")
		if err == nil {
			t.Error("changed cached result accepted")
		} else if got := fixture.ExtractDiagnostic(t, err); got != d {
			t.Errorf("cached result diagnostic=%#v want=%#v", got, d)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: fixed, SQL: "SELECT id FROM t ORDER BY id"}, b.Recorder, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		result, err = insert.ExecContext(p.Ctx, int64(22), "new")
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		run(fixture.Exec("ALTER TYPE pair RENAME ATTRIBUTE flag TO enabled CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "note", "enabled"}, []string{"INT8", "TEXT", "BOOL"}, [][]any{{int64(11), "hey", nil}, {int64(22), "new", nil}}))
		rows, err = fixed.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Error("changed fixed result exposed cursor")
		}
		if err == nil {
			t.Error("changed fixed result accepted")
		} else if got := fixture.ExtractDiagnostic(t, err); got != d {
			t.Errorf("fixed result diagnostic=%#v want=%#v", got, d)
		}
	})
}
