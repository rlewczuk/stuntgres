package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsLifecycle(t *testing.T) {
	icScenario(t, func(c *icCase) {
		t := c.t
		c.exec("CREATE TABLE ic_scope.life(a integer NOT NULL DEFAULT 7,b text,c varchar(3))")
		where := "table_schema IN ('ic_scope','ic_moved') AND table_name IN ('life','renamed')"
		held := c.hold(icProjection(c.b.PG, where, nil).Query)
		check := func(want ...[]any) {
			s := icProjection(c.b.PG, where, want)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		a := icPatch(c.integer("life", "a", 1), map[string]any{"is_nullable": "NO", "column_default": "7"})
		b := c.row("life", "b", 2, "text", "text", map[string]any{"character_octet_length": int64(1073741824)})
		cc := c.row("life", "c", 3, "character varying", "varchar", map[string]any{"character_maximum_length": int64(3), "character_octet_length": int64(12)})
		check(a, b, cc)
		// Native cursors are eager snapshots; pq is drained before another command.
		var retained = func() func() {
			if c.b.PG {
				return func() {}
			}
			rows, err := c.conn.QueryContext(c.p.Ctx, icProjection(false, where, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			return func() { c.consume(rows, [][]any{a, b, cc}) }
		}()
		c.exec("ALTER TABLE ic_scope.life DROP COLUMN b")
		check(a, cc)
		c.exec("ALTER TABLE ic_scope.life ADD COLUMN d boolean")
		d := c.row("life", "d", 4, "boolean", "bool", nil)
		check(a, cc, d)
		retained()
		c.exec("ALTER TABLE ic_scope.life RENAME COLUMN c TO word")
		cc = icPatch(cc, map[string]any{"column_name": "word"})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN word TYPE varchar(7)")
		cc = icPatch(cc, map[string]any{"character_maximum_length": int64(7), "character_octet_length": int64(28)})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a DROP DEFAULT")
		a = icPatch(a, map[string]any{"column_default": nil})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a DROP NOT NULL")
		a = icPatch(a, map[string]any{"is_nullable": "YES"})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a SET DEFAULT 9")
		a = icPatch(a, map[string]any{"column_default": "9"})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a SET NOT NULL")
		a = icPatch(a, map[string]any{"is_nullable": "NO"})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a SET DEFAULT NULL")
		a = icPatch(a, map[string]any{"column_default": nil})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life ALTER COLUMN a TYPE bigint")
		a = icPatch(a, map[string]any{"data_type": "bigint", "udt_name": "int8", "numeric_precision": int64(64)})
		check(a, cc, d)
		c.exec("ALTER TABLE ic_scope.life RENAME TO renamed")
		a = icPatch(a, map[string]any{"table_name": "renamed"})
		cc = icPatch(cc, map[string]any{"table_name": "renamed"})
		d = icPatch(d, map[string]any{"table_name": "renamed"})
		check(a, cc, d)
		c.exec("CREATE SCHEMA ic_moved", "ALTER TABLE ic_scope.renamed SET SCHEMA ic_moved")
		a = icPatch(a, map[string]any{"table_schema": "ic_moved"})
		cc = icPatch(cc, map[string]any{"table_schema": "ic_moved"})
		d = icPatch(d, map[string]any{"table_schema": "ic_moved"})
		check(a, cc, d)
		c.run(c.conn, fixture.Exec("INSERT INTO ic_moved.renamed VALUES(1,'abc',true)", 1))
		diag := fixture.ControlsReferenceDiagnostic("42701", `column "a" of relation "renamed" already exists`)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE ic_moved.renamed ADD COLUMN a integer", diag)
			s.Prepared, s.ErrorStage = prepared, "execute"
			c.run(c.conn, s)
			check(a, cc, d)
			c.run(c.conn, fixture.Select("SELECT * FROM ic_moved.renamed", []string{"a", "word", "d"}, []string{"INT8", "VARCHAR", "BOOL"}, [][]any{{int64(1), "abc", true}}))
		}
	})
}
