package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableOtherParentRetention(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE other(id integer)", 0))
		run(fixture.Exec("CREATE TABLE child() INHERITS(t,other)", 0))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE id CASCADE", 0))
		if b.PG {
			fixture.TableDDLMetadata(t, p.Ctx, c, true, prepared, "SELECT * FROM ONLY t LIMIT 0", []string{"note"}, []string{"TEXT"}, []bool{true}, 0)
		} else {
			fixture.CompositeReferenceError(t, p, c, "SELECT * FROM t LIMIT 0", "prepare", typedTableError("0A000", "recursive table inheritance is unsupported"))
		}
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM child LIMIT 0", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 0)
		if b.PG {
			fixture.TableDDLMetadata(t, p.Ctx, c, true, prepared, "SELECT * FROM ONLY other LIMIT 0", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
		} else {
			fixture.CompositeReferenceError(t, p, c, "SELECT * FROM other LIMIT 0", "prepare", typedTableError("0A000", "recursive table inheritance is unsupported"))
		}
		run(typedTableAttachment("t", "pair", true))
		run(fixture.Exec("INSERT INTO child VALUES(11,'retained')", 1))
		run(fixture.Select("SELECT * FROM child", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "retained"}}))
	})
}
