package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableAttachment(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (gone text,id integer,note varchar(5))", 0))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE gone", 0))
		run(fixture.Exec("CREATE TYPE replacement AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t(id integer DEFAULT 7 NOT NULL,discard boolean,note varchar(5) DEFAULT 'ok',CONSTRAINT positive CHECK(id>0))", 0))
		run(fixture.Exec("ALTER TABLE t DROP COLUMN discard", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		run(fixture.Exec("ALTER TABLE t OF pair", 0))
		run(fixture.TypedTableAttachment("t", "pair", true))
		run(fixture.Exec("ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id", "label"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(11), "hey"}}))
		run(fixture.Exec("ALTER TYPE pair RENAME ATTRIBUTE label TO note CASCADE", 0))
		run(fixture.Exec("ALTER TABLE t OF replacement", 0))
		run(fixture.TypedTableAttachment("t", "replacement", true))
		run(fixture.Exec("DROP TYPE pair", 0))
		run(fixture.Exec("ALTER TABLE t NOT OF", 0))
		run(fixture.TypedTableAttachment("t", "replacement", false))
		run(fixture.Exec("DROP TYPE replacement", 0))
		run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
		run(typedTablePairRows("t", [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, []bool{false, true}, 2)
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE t NOT OF", "execute", typedTableError("42809", `"t" is not a typed table`))
		run(fixture.Exec("ALTER TABLE t ADD COLUMN extra boolean DEFAULT true", 0))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "note", "extra"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(7), "ok", true}, {int64(11), "hey", true}}))
	})
}

func TestReferenceTypedTableAttachmentMismatch(t *testing.T) {
	for _, tc := range []struct{ name, declaration, message string }{
		{"missing", "id integer", `table is missing column "note"`},
		{"extra", "id integer,note varchar(5),extra boolean", `table has extra column "extra"`},
		{"order", "note varchar(5),id integer", `table has column "note" where type requires "id"`},
		{"type", "id bigint,note varchar(5)", `table "t" has different type for column "id"`},
		{"typmod", "id integer,note varchar(6)", `table "t" has different type for column "note"`},
		{"collation", `id integer,note varchar(5) COLLATE "POSIX"`, `table "t" has different type for column "note"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
				run(fixture.Exec("CREATE TABLE t("+tc.declaration+")", 0))
				fixture.CompositeReferenceError(t, p, c, "ALTER TABLE t OF pair", "execute", typedTableError("42804", tc.message))
				run(fixture.TypedTableAttachment("t", "pair", false))
			})
		})
	}
}

func TestReferenceTypedTableAttachmentNamedMemberIdentity(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE a AS (v integer)", 0))
		run(fixture.Exec("CREATE TYPE z AS (v integer)", 0))
		run(fixture.Exec("CREATE TYPE pair AS (v a)", 0))
		run(fixture.Exec("CREATE TABLE t(v z)", 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE t OF pair", "execute", typedTableError("42804", `table "t" has different type for column "v"`))
		run(fixture.Exec("CREATE TABLE parent(v a)", 0))
		run(fixture.Exec("CREATE TABLE child() INHERITS(parent)", 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE child OF pair", "execute", typedTableError("42809", "typed tables cannot inherit"))
	})
}
