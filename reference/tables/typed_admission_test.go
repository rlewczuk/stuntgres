package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableExplicitTransactionBoundary(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE ordinary(id integer)", 0))
		for _, q := range []string{"ALTER TABLE ordinary OF pair", "ALTER TABLE t NOT OF", "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", "DROP TYPE pair CASCADE"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(q, 0))
			switch q {
			case "ALTER TABLE ordinary OF pair":
				run(fixture.TypedTableAttachment("ordinary", "pair", true))
			case "ALTER TABLE t NOT OF":
				run(fixture.TypedTableAttachment("t", "pair", false))
			case "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE":
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, 0)
			case "DROP TYPE pair CASCADE":
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='t'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.TypedTableAttachment("t", "pair", true))
			run(fixture.TypedTableAttachment("ordinary", "pair", false))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
		}
	})
}
