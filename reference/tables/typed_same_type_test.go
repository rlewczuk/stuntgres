package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableOwnRowStoredTypeGuard(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE holder(v t)", 0))
		run(fixture.Exec("INSERT INTO holder VALUES($1::t)", 1, "(11)"))
		// Same-usage TYPE still propagates an actual TYPE action, protecting the
		// distinct typed table's stored row codec as well as the source codec.
		for _, destination := range []string{"integer", "bigint"} {
			fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE "+destination+" CASCADE", "execute", typedTableError("0A000", `cannot alter table "t" because column "holder.v" uses its row type`))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
			fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(11)"}}, prepared)
			run(fixture.TypedTableAttachment("t", "pair", true))
		}
		run(fixture.Exec("CREATE TABLE probe OF pair", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM probe", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
	})
}
