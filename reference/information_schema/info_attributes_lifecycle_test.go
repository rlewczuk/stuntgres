package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func attrConsume(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, pg bool, want [][]any) {
	t.Helper()
	attrMetadata(t, types, pg, attrColumns, attrTypes)
	var got [][]any
	for rows.Next() {
		values, pointers := make([]any, 31), make([]any, 31)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rows=%#v want=%#v err=%v", got, want, rows.Err())
	}
	attrMetadata(t, types, pg, attrColumns, attrTypes)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	attrMetadata(t, types, pg, attrColumns, attrTypes)
}

func TestReferenceInfoAttributesLifecycle(t *testing.T) {
	t.Run("ddl", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE SCHEMA attr_scope", "CREATE SCHEMA attr_moved", "CREATE TYPE attr_scope.pair AS (first integer,middle text,last bigint)")
			where := "udt_schema IN ('attr_scope','attr_moved')"
			h := c.hold(where)
			check := func(want ...[]any) { c.held(h, where, nil, want...) }
			first := c.integer("pair", "first", 1)
			middle := attrPatch(attrRow(c.database, "attr_scope", "pair", "middle", 2, "text", "pg_catalog", "text"), map[string]any{"character_octet_length": int64(1073741824)})
			last := attrPatch(attrRow(c.database, "attr_scope", "pair", "last", 3, "bigint", "pg_catalog", "int8"), map[string]any{"numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)})
			check(first, middle, last)
			old, err := c.c.QueryContext(c.p.Ctx, attrProjection(c.b.PG, where, nil).Query)
			if err != nil {
				c.t.Fatal(err)
			}
			defer old.Close()
			types, err := old.ColumnTypes()
			if err != nil {
				c.t.Fatal(err)
			}
			peer, err := c.b.DB.Conn(c.p.Ctx)
			if err != nil {
				c.t.Fatal(err)
			}
			defer peer.Close()
			fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, fixture.Exec("CREATE TYPE attr_scope.empty AS ()", 0))
			fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, fixture.Exec("ALTER TYPE attr_scope.pair DROP ATTRIBUTE middle", 0))
			attrConsume(c.t, old, types, c.b.PG, [][]any{first, middle, last})
			check(first, last)
			c.exec("ALTER TYPE attr_scope.pair ADD ATTRIBUTE later varchar(2)")
			later := attrPatch(attrRow(c.database, "attr_scope", "pair", "later", 4, "character varying", "pg_catalog", "varchar"), map[string]any{"character_maximum_length": int64(2), "character_octet_length": int64(8)})
			check(first, last, later)
			c.exec("ALTER TYPE attr_scope.pair RENAME ATTRIBUTE first TO renamed", "ALTER TYPE attr_scope.pair ALTER ATTRIBUTE last TYPE integer")
			first = c.integer("pair", "renamed", 1)
			last = c.integer("pair", "last", 3)
			check(first, last, later)
			c.exec("ALTER TYPE attr_scope.pair RENAME TO renamed", "ALTER TYPE attr_scope.renamed SET SCHEMA attr_moved")
			for _, row := range [][]any{first, last, later} {
				row[1], row[2] = "attr_moved", "renamed"
			}
			check(first, last, later)
			c.exec("DROP TYPE attr_moved.renamed")
			check()
			c.exec("CREATE TYPE attr_moved.renamed AS (fresh boolean)")
			check(attrRow(c.database, "attr_moved", "renamed", "fresh", 1, "boolean", "pg_catalog", "bool"))
			attrMetadata(c.t, types, c.b.PG, attrColumns, attrTypes)
		})
	})
	t.Run("rollback", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)")
			where := "udt_schema='attr_scope'"
			h := c.hold(where)
			original := c.integer("pair", "id", 1)
			c.held(h, where, nil, original)
			peer, err := c.b.DB.Conn(c.p.Ctx)
			if err != nil {
				c.t.Fatal(err)
			}
			defer peer.Close()
			peerCheck := func() {
				s := attrProjection(c.b.PG, where, [][]any{original})
				fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, s)
				fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, fixture.Prepared(s))
			}
			c.exec("BEGIN", "CREATE TYPE attr_scope.private AS (flag boolean)", "ALTER TYPE attr_scope.pair ADD ATTRIBUTE label varchar(2)")
			label := attrPatch(attrRow(c.database, "attr_scope", "pair", "label", 2, "character varying", "pg_catalog", "varchar"), map[string]any{"character_maximum_length": int64(2), "character_octet_length": int64(8)})
			private := attrRow(c.database, "attr_scope", "private", "flag", 1, "boolean", "pg_catalog", "bool")
			c.held(h, where, nil, original, label, private)
			peerCheck()
			c.exec("DROP TYPE attr_scope.pair")
			c.held(h, where, nil, private)
			peerCheck()
			c.exec("ROLLBACK")
			c.held(h, where, nil, original)
			peerCheck()
		})
	})
	t.Run("acl_rollback_recreate", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE ROLE attr_owner", "CREATE ROLE attr_reader", "CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)", "ALTER TYPE attr_scope.pair OWNER TO attr_owner", "REVOKE ALL ON TYPE attr_scope.pair FROM PUBLIC")
			where := "udt_schema='attr_scope' AND udt_name='pair'"
			h := c.hold(where)
			original := c.integer("pair", "id", 1)
			c.exec("SET ROLE attr_reader")
			c.held(h, where, nil)
			c.exec("RESET ROLE", "BEGIN", "GRANT USAGE ON TYPE attr_scope.pair TO attr_reader", "SET ROLE attr_reader")
			c.held(h, where, nil, original)
			c.exec("RESET ROLE", "ROLLBACK", "SET ROLE attr_reader")
			c.held(h, where, nil)
			c.exec("RESET ROLE", "GRANT USAGE ON TYPE attr_scope.pair TO attr_reader", "BEGIN", "REVOKE USAGE ON TYPE attr_scope.pair FROM attr_reader", "SET ROLE attr_reader")
			c.held(h, where, nil)
			c.exec("RESET ROLE", "ROLLBACK", "SET ROLE attr_reader")
			c.held(h, where, nil, original)
			c.exec("RESET ROLE", "DROP TYPE attr_scope.pair", "CREATE TYPE attr_scope.pair AS (fresh boolean)", "REVOKE ALL ON TYPE attr_scope.pair FROM PUBLIC", "SET ROLE attr_reader")
			c.held(h, where, nil)
			c.exec("RESET ROLE")
			c.held(h, where, nil, attrRow(c.database, "attr_scope", "pair", "fresh", 1, "boolean", "pg_catalog", "bool"))
		})
	})
}
