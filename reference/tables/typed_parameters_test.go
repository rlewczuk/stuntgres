package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableSourceParameterShape(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE holder(v pair)", 0))
		stmt, err := c.PrepareContext(p.Ctx, "INSERT INTO holder VALUES($1::pair)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		result, err := stmt.ExecContext(p.Ctx, "(11,hey)")
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		result, err = stmt.ExecContext(p.Ctx, "(22,new,t)")
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(11,hey,)"}, {"(22,new,t)"}}, prepared)
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE note CASCADE", 0))
		result, err = stmt.ExecContext(p.Ctx, "(33,f)")
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(11,)"}, {"(22,t)"}, {"(33,f)"}}, prepared)
	})
}

func TestReferenceTypedTableBoundBufferOwnership(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		payload := []byte("first")
		run(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, int64(11), payload))
		copy(payload, "other")
		run(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, int64(22), payload))
		copy(payload, "xxxxx")
		run(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, int64(33), nil))
		run(typedTablePairRows("t", [][]any{{int64(11), "first"}, {int64(22), "other"}, {int64(33), nil}}))
	})
}

func TestReferenceTypedTableNestedStoredBoundary(t *testing.T) {
	for _, declaration := range []string{"wrapped", "pair[]", "typed_box"} {
		t.Run(declaration, func(t *testing.T) {
			typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
				run(fixture.Exec("CREATE DOMAIN wrapped AS pair", 0))
				run(fixture.Exec("CREATE TYPE typed_box AS (v pair)", 0))
				run(fixture.Exec("CREATE TABLE t OF pair", 0))
				if declaration == "pair[]" && !b.PG {
					// SQL storage of composite arrays remains outside the admitted
					// transport boundary; the real array-provider guard has package proof.
					d := typedTableError("0A000", "unsupported column type")
					d.Position = "23"
					fixture.CompositeReferenceError(t, p, c, "CREATE TABLE holder(v pair[])", "execute", d)
					run(typedTableAttachment("t", "pair", true))
					fixture.TableDDLMetadata(t, p.Ctx, c, false, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
					return
				}
				run(fixture.Exec("CREATE TABLE holder(v "+declaration+")", 0))
				fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", "execute", typedTableError("0A000", `cannot alter type "pair" because column "holder.v" uses it`))
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
			})
		})
	}
}

func TestReferenceTypedTableNestedPopulatedConsumer(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TYPE typed_box AS (v pair)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE holder(v public.typed_box)", 0))
		run(fixture.Exec("INSERT INTO holder VALUES($1::public.typed_box)", 1, `("(11)")`))
		// Distinct from builtin pg_catalog.box: this is an actual populated
		// standalone composite containing pair, using bound record input.
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", "execute", typedTableError("0A000", `cannot alter type "pair" because column "holder.v" uses it`))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{`("(11)")`}}, prepared)
	})
}
