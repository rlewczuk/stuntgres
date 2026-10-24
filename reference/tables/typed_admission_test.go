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
			if b.PG || q == "ALTER TABLE ordinary OF pair" || q == "ALTER TABLE t NOT OF" {
				run(fixture.Exec(q, 0))
			} else {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("ROLLBACK", 0))
			run(typedTableAttachment("t", "pair", true))
			run(typedTableAttachment("ordinary", "pair", false))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
		}
	})
}
