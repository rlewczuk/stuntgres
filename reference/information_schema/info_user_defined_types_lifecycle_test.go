package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserDefinedTypesLifecycle(t *testing.T) {
	t.Run("ddl", func(t *testing.T) {
		udtScenario(t, func(c *udtCase) {
			c.exec("CREATE SCHEMA udt_scope", "CREATE SCHEMA udt_moved")
			where := "user_defined_type_schema IN ('udt_scope','udt_moved','udt_private')"
			h := c.hold(where)
			check := func(want ...[]any) { c.check(where, want...); c.held(h, where, nil, want...) }
			check()
			c.exec("CREATE TYPE udt_scope.alpha AS (v integer)")
			alpha := udtRow(c.database, "udt_scope", "alpha")
			check(alpha)
			// Retain real public rows and their descriptors across a peer DDL frontier.
			rows, err := c.c.QueryContext(c.p.Ctx, udtProjection(c.b.PG, where, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			udtMetadata(t, types, c.b.PG)
			if !rows.Next() {
				t.Fatalf("missing saved row: %v", rows.Err())
			}
			saved := make([]any, 29)
			ptrs := make([]any, 29)
			for i := range saved {
				ptrs[i] = &saved[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range saved {
				saved[i] = fixture.Normalize(v)
			}
			if !reflect.DeepEqual(saved, alpha) {
				t.Fatalf("saved=%#v", saved)
			}
			peer, err := c.b.DB.Conn(c.p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			fixture.RunStep(t, c.p.Ctx, peer, c.b.Recorder, fixture.Exec("CREATE TYPE udt_scope.empty AS ()", 0))
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("saved result changed: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			udtMetadata(t, types, c.b.PG)
			empty := udtRow(c.database, "udt_scope", "empty")
			check(alpha, empty)
			c.exec("ALTER TYPE udt_scope.alpha ADD ATTRIBUTE extra varchar(7)")
			check(alpha, empty)
			c.exec("ALTER TYPE udt_scope.alpha RENAME TO renamed")
			renamed := udtRow(c.database, "udt_scope", "renamed")
			check(renamed, empty)
			c.exec("ALTER TYPE udt_scope.renamed SET SCHEMA udt_moved")
			moved := udtRow(c.database, "udt_moved", "renamed")
			check(moved, empty)
			c.exec("ALTER SCHEMA udt_moved RENAME TO udt_private")
			moved = udtRow(c.database, "udt_private", "renamed")
			check(moved, empty)
			c.exec("DROP TYPE udt_scope.empty")
			check(moved)
			if !reflect.DeepEqual(saved, alpha) {
				t.Fatal("saved row changed", saved)
			}
			udtMetadata(t, types, c.b.PG)
		})
	})
	t.Run("rollback", func(t *testing.T) {
		udtScenario(t, func(c *udtCase) {
			c.exec("CREATE SCHEMA udt_scope", "CREATE SCHEMA udt_moved", "CREATE TYPE udt_scope.alpha AS (v integer)")
			where := "user_defined_type_schema IN ('udt_scope','udt_moved')"
			h := c.hold(where)
			alpha := udtRow(c.database, "udt_scope", "alpha")
			check := func(want ...[]any) { c.check(where, want...); c.held(h, where, nil, want...) }
			check(alpha)
			c.exec("BEGIN", "CREATE TYPE udt_scope.empty AS ()")
			check(alpha, udtRow(c.database, "udt_scope", "empty"))
			c.exec("ALTER TYPE udt_scope.alpha RENAME TO renamed", "ALTER TYPE udt_scope.renamed SET SCHEMA udt_moved")
			check(udtRow(c.database, "udt_moved", "renamed"), udtRow(c.database, "udt_scope", "empty"))
			c.exec("DROP TYPE udt_moved.renamed")
			check(udtRow(c.database, "udt_scope", "empty"))
			c.exec("ROLLBACK")
			check(alpha)
		})
	})
	t.Run("acl_rollback_and_owner", func(t *testing.T) {
		udtScenario(t, func(c *udtCase) {
			c.exec("CREATE ROLE udt_reader", "CREATE ROLE udt_owner", "CREATE SCHEMA udt_scope", "CREATE TYPE udt_scope.alpha AS ()", "ALTER TYPE udt_scope.alpha OWNER TO udt_owner", "REVOKE ALL ON TYPE udt_scope.alpha FROM PUBLIC")
			where := "user_defined_type_schema='udt_scope'"
			h := c.hold(where)
			check := func(want ...[]any) { c.check(where, want...); c.held(h, where, nil, want...) }
			alpha := udtRow(c.database, "udt_scope", "alpha")
			c.exec("SET ROLE udt_reader")
			check()
			c.exec("RESET ROLE", "BEGIN", "GRANT USAGE ON TYPE udt_scope.alpha TO udt_reader", "SET ROLE udt_reader")
			check(alpha)
			c.exec("RESET ROLE", "ROLLBACK", "SET ROLE udt_reader")
			check()
			c.exec("RESET ROLE", "ALTER TYPE udt_scope.alpha OWNER TO udt_reader", "SET ROLE udt_reader")
			check(alpha)
			c.exec("RESET ROLE", "ALTER TYPE udt_scope.alpha OWNER TO udt_owner", "SET ROLE udt_reader")
			check()
			c.exec("RESET ROLE", "GRANT USAGE ON TYPE udt_scope.alpha TO udt_reader", "BEGIN", "REVOKE USAGE ON TYPE udt_scope.alpha FROM udt_reader", "SET ROLE udt_reader")
			check()
			c.exec("RESET ROLE", "ROLLBACK", "SET ROLE udt_reader")
			check(alpha)
			c.exec("RESET ROLE")
		})
	})
}
