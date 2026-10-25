package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableLocalConstraintsLifecycle(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair (id DEFAULT 7 NOT NULL,CONSTRAINT positive CHECK(id>0),CONSTRAINT note_key UNIQUE(note))", 0))
		run(fixture.Exec("CREATE INDEX t_id_idx ON t(id)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		run(fixture.Exec("ALTER TABLE t ALTER COLUMN note SET DEFAULT 'ok'", 0))
		run(fixture.Exec("ALTER TABLE t ALTER COLUMN note SET NOT NULL", 0))
		run(fixture.Exec("ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
		run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT8", "TEXT"}, []bool{false, false}, 2)
		d := typedTableError("23514", `new row for relation "t" violates check constraint "positive"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (-1, bad).", "public", "t", "positive"
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO t VALUES(-1,'bad')", "execute", d)
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE note CASCADE", 0))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE tablename='t' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"t_id_idx"}}))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(11)}}))
		run(fixture.Exec("ALTER TABLE t NOT OF", 0))
		run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(7)}, {int64(11)}}))
	})
}

func TestReferenceTypedTableAtomicityIncompatibleCast(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE a OF pair", 0))
		run(fixture.Exec("CREATE TABLE z OF pair (id DEFAULT 7)", 0))
		run(fixture.Exec("INSERT INTO a VALUES(11)", 1))
		run(fixture.Exec("INSERT INTO z DEFAULT VALUES", 1))
		d := typedTableError("42804", `column "id" cannot be cast automatically to type boolean`)
		d.Hint = `You might need to specify "USING id::boolean".`
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE boolean CASCADE", "execute", d)
		for _, table := range []string{"a", "z"} {
			value := int64(11)
			if table == "z" {
				value = 7
			}
			run(fixture.Select("SELECT * FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{value}}))
			run(fixture.TypedTableAttachment(table, "pair", true))
		}
		run(fixture.Exec("INSERT INTO z DEFAULT VALUES", 1))
	})
}

func TestReferenceTypedTableRetainedDefaultNarrowing(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE a OF pair (id DEFAULT 7)", 0))
		run(fixture.Exec("CREATE TABLE z OF pair (id DEFAULT 40000)", 0))
		run(fixture.Exec("INSERT INTO a VALUES(11)", 1))
		run(fixture.Exec("INSERT INTO z VALUES(11)", 1))
		// Pinned PostgreSQL accepts narrowing fitting rows; evaluation of the
		// retained overflowing default is deferred until the later INSERT.
		run(fixture.Exec("ALTER TYPE pair ALTER ATTRIBUTE id TYPE smallint CASCADE", 0))
		for _, table := range []string{"a", "z"} {
			run(fixture.Select("SELECT * FROM "+table, []string{"id"}, []string{"INT2"}, [][]any{{int64(11)}}))
			run(fixture.TypedTableAttachment(table, "pair", true))
		}
		run(fixture.Exec("INSERT INTO a DEFAULT VALUES", 1))
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO z DEFAULT VALUES", "execute", typedTableError("22003", "smallint out of range"))
		run(fixture.Select("SELECT * FROM a ORDER BY id", []string{"id"}, []string{"INT2"}, [][]any{{int64(7)}, {int64(11)}}))
		run(fixture.Select("SELECT * FROM z ORDER BY id", []string{"id"}, []string{"INT2"}, [][]any{{int64(11)}}))
		run(fixture.Exec("CREATE TABLE probe OF pair", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM probe", []string{"id"}, []string{"INT2"}, []bool{true}, 0)
	})
}
