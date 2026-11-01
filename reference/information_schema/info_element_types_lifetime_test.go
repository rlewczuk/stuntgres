package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesLifetime(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		where := "object_schema IN ('public','et_moved') AND object_name IN ('et_life','et_life_new','et_life_pair','et_life_pair_new')"
		step := func(rows [][]any) fixture.Step { return etProjection(b.PG, where, rows) }
		held := etHeld(t, p, b, c, step(nil).Query)
		held(step(nil))
		etExec(t, p, b, c, "CREATE TABLE et_life(a integer[],gone text[],z bigint[])", "CREATE TYPE et_life_pair AS (a integer[],s integer,b text[])")
		initial := [][]any{
			etRow(database, "public", "et_life", "TABLE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_life", "TABLE", 2, "text", "pg_catalog", "text"),
			etRow(database, "public", "et_life", "TABLE", 3, "bigint", "pg_catalog", "int8"),
			etRow(database, "public", "et_life_pair", "USER-DEFINED TYPE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_life_pair", "USER-DEFINED TYPE", 3, "text", "pg_catalog", "text"),
		}
		held(step(initial))
		old, err := c.QueryContext(p.Ctx, step(nil).Query+" ORDER BY object_name,collection_type_identifier")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		names, err := old.Columns()
		if err != nil {
			t.Fatal(err)
		}
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		etMetadata(t, types, b.PG)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		etExec(t, p, b, peer,
			"ALTER TABLE et_life DROP COLUMN gone", "ALTER TABLE et_life ADD COLUMN added text[]",
			"ALTER TABLE et_life RENAME TO et_life_new", "CREATE SCHEMA et_moved", "ALTER TABLE et_life_new SET SCHEMA et_moved",
			"ALTER TYPE et_life_pair DROP ATTRIBUTE a", "ALTER TYPE et_life_pair ADD ATTRIBUTE added bigint[]",
			"ALTER TYPE et_life_pair RENAME TO et_life_pair_new", "ALTER TYPE et_life_pair_new SET SCHEMA et_moved",
		)
		etConsume(t, old, types, b.PG, initial)
		if !reflect.DeepEqual(names, etColumns) {
			t.Fatalf("retained columns=%v want=%v", names, etColumns)
		}
		current := [][]any{
			etRow(database, "et_moved", "et_life_new", "TABLE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, "et_moved", "et_life_new", "TABLE", 3, "bigint", "pg_catalog", "int8"),
			etRow(database, "et_moved", "et_life_new", "TABLE", 4, "text", "pg_catalog", "text"),
			etRow(database, "et_moved", "et_life_pair_new", "USER-DEFINED TYPE", 3, "text", "pg_catalog", "text"),
			etRow(database, "et_moved", "et_life_pair_new", "USER-DEFINED TYPE", 4, "bigint", "pg_catalog", "int8"),
		}
		etCheck(t, p, b, c, step(current))
		held(step(current))
		etMetadata(t, types, b.PG)
		// Current authority is recaptured, not frozen by the held analysis.
		etExec(t, p, b, c, "CREATE ROLE et_reader", "REVOKE USAGE ON TYPE et_moved.et_life_pair_new FROM PUBLIC", "SET ROLE et_reader")
		held(step(nil))
		etExec(t, p, b, c, "RESET ROLE", "GRANT SELECT(a) ON TABLE et_moved.et_life_new TO et_reader", "SET ROLE et_reader")
		held(step(current[:1]))
		etExec(t, p, b, c, "RESET ROLE", "REVOKE SELECT(a) ON TABLE et_moved.et_life_new FROM et_reader", "SET ROLE et_reader")
		held(step(nil))
		etExec(t, p, b, c, "RESET ROLE", "DROP TABLE et_moved.et_life_new", "DROP TYPE et_moved.et_life_pair_new")
		held(step(nil))
		etMetadata(t, types, b.PG)
		bound := etProjection(b.PG, "object_name=$1", nil)
		bound.Args = []any{nil}
		etCheck(t, p, b, c, bound)
	})
}
