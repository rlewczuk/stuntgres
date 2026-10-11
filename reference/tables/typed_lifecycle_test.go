package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableLifecycle(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		for _, table := range []string{"t", "u"} {
			run(fixture.Exec("CREATE TABLE "+table+" OF pair", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES(11,'hey')", 1))
		}
		run(fixture.Exec("CREATE TABLE detached OF pair", 0))
		run(fixture.Exec("ALTER TABLE detached NOT OF", 0))
		run(fixture.Exec("INSERT INTO detached VALUES(22,'det')", 1))
		run(fixture.Exec("CREATE TABLE copied(LIKE t)", 0))
		for _, q := range []string{"ALTER TYPE pair ADD ATTRIBUTE flag boolean", "ALTER TYPE pair ADD ATTRIBUTE flag boolean RESTRICT", "ALTER TYPE pair DROP ATTRIBUTE note RESTRICT", "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint RESTRICT", "ALTER TYPE pair RENAME ATTRIBUTE note TO label RESTRICT"} {
			fixture.CompositeReferenceError(t, p, c, q, "execute", typedTableRestriction("pair"))
		}
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		for _, table := range []string{"t", "u"} {
			run(fixture.Select("SELECT * FROM "+table, []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(11), "hey", nil}}))
		}
		run(fixture.Exec("ALTER TYPE pair RENAME ATTRIBUTE flag TO enabled CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE note CASCADE", 0))
		for _, table := range []string{"t", "u"} {
			run(fixture.Select("SELECT * FROM "+table, []string{"id", "enabled"}, []string{"INT8", "BOOL"}, [][]any{{int64(11), nil}}))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM "+table, []string{"id", "enabled"}, []string{"INT8", "BOOL"}, []bool{true, true}, 1)
			run(typedTableAttachment(table, "pair", true))
		}
		run(typedTablePairRows("detached", [][]any{{int64(22), "det"}}))
		run(typedTablePairRows("copied", nil))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE enabled CASCADE, ADD ATTRIBUTE note text CASCADE, ALTER ATTRIBUTE id TYPE numeric CASCADE", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "note"}, []string{"NUMERIC", "TEXT"}, [][]any{{"11", nil}}))
	})
}

func TestReferenceTypedTableDropDependencies(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		run(fixture.Exec("CREATE TABLE copied(LIKE t)", 0))
		d := typedTableError("2BP01", "cannot drop type pair because other objects depend on it")
		d.Detail, d.Hint = "table t depends on type pair", "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.CompositeReferenceError(t, p, c, "DROP TYPE pair", "execute", d)
		run(typedTablePairRows("t", [][]any{{int64(11), "hey"}}))
		fixture.CompositeReferenceError(t, p, c, "DROP TYPE pair, missing CASCADE", "execute", typedTableError("42704", `type "missing" does not exist`))
		run(typedTableAttachment("t", "pair", true))
		run(fixture.Exec("DROP TYPE pair CASCADE", 0))
		d = typedTableError("42P01", `relation "t" does not exist`)
		d.Position = "15"
		fixture.CompositeReferenceError(t, p, c, "SELECT * FROM t", "prepare", d)
		run(typedTablePairRows("copied", nil))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='pair' OR relname='t'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE TYPE a AS (id integer)", 0))
		run(fixture.Exec("CREATE TYPE z AS (id integer)", 0))
		run(fixture.Exec("DROP TYPE IF EXISTS missing,a,z", 0))
	})
}

func TestReferenceTypedTableStoredCompositeBoundary(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE holder(v pair)", 0))
		run(fixture.Exec("INSERT INTO holder VALUES('(11,hey)')", 1))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", "execute", typedTableError("0A000", `cannot alter type "pair" because column "holder.v" uses it`))
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(11,hey,)"}}, prepared)
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE note CASCADE", 0))
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(11,)"}}, prepared)
	})
}
