package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintTableUsageLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ctu_life_target", "ctu_life_child_schema")
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_life(a integer,dead text,b integer,CONSTRAINT ctu_life_u UNIQUE(b,a))", "ALTER TABLE ctu_life DROP COLUMN dead",
			"CREATE TABLE ctu_life_child(x integer NOT NULL,y integer,CHECK(x>0 AND y>0),CONSTRAINT ctu_life_fk FOREIGN KEY(x,y) REFERENCES ctu_life(b,a))",
			"CREATE TABLE ctu_life_child_two(x integer,y integer,CONSTRAINT ctu_life_fk FOREIGN KEY(x,y) REFERENCES ctu_life(a,b))")
		held := c.hold(c.conn, ctuQuery)
		targetSchema, childSchema, table, unique, fk := "public", "public", "ctu_life", "ctu_life_u", "ctu_life_fk"
		hasUnique, hasFK, hasSecond := true, true, true
		bag := func() [][]any {
			var rows [][]any
			if hasUnique {
				rows = append(rows, c.row(targetSchema, table, targetSchema, unique))
			}
			if hasFK {
				rows = append(rows, c.row(targetSchema, table, childSchema, fk))
			}
			if hasSecond {
				rows = append(rows, c.row(targetSchema, table, "public", "ctu_life_fk"))
			}
			return rows
		}
		check := func() { c.routes(held, "ctu_life%", bag()...) }
		check()
		c.exec("ALTER TABLE ctu_life RENAME COLUMN b TO key_b")
		check()
		key := fixture.Select("SELECT column_name,ordinal_position FROM information_schema.key_column_usage WHERE table_name='ctu_life' AND constraint_name='ctu_life_u' ORDER BY ordinal_position", []string{"column_name", "ordinal_position"}, []string{"NAME", "INT4"}, [][]any{{"key_b", int64(1)}, {"a", int64(2)}})
		c.run(c.conn, key)
		c.run(c.conn, fixture.Prepared(key))
		rename := func() {
			c.exec("ALTER TABLE ctu_life RENAME CONSTRAINT ctu_life_u TO ctu_life_renamed_u", "ALTER TABLE ctu_life_child RENAME CONSTRAINT ctu_life_fk TO ctu_life_renamed_fk")
		}
		if c.b.PG {
			rename()
		} else {
			old, err := c.conn.QueryContext(c.p.Ctx, ctuQuery, "ctu_life%")
			if err != nil {
				c.t.Fatal(err)
			}
			defer old.Close()
			saved, want := ctuMetadata(c.t, old, false), bag()
			rename()
			ctuAssertRows(c.t, old, want, false)
			ctuColumnTypes(c.t, saved, false)
		}
		unique, fk = "ctu_life_renamed_u", "ctu_life_renamed_fk"
		check()
		c.exec("ALTER TABLE ctu_life RENAME TO ctu_life_renamed")
		table = "ctu_life_renamed"
		check()
		c.exec("CREATE SCHEMA ctu_life_target", "CREATE SCHEMA ctu_life_child_schema", "ALTER TABLE ctu_life_renamed SET SCHEMA ctu_life_target")
		targetSchema = "ctu_life_target"
		check()
		c.exec("ALTER TABLE ctu_life_child SET SCHEMA ctu_life_child_schema")
		childSchema = "ctu_life_child_schema"
		check()
		c.exec("ALTER TABLE ctu_life_child_schema.ctu_life_child DROP CONSTRAINT ctu_life_renamed_fk")
		hasFK = false
		check()
		c.exec("ALTER TABLE ctu_life_child_schema.ctu_life_child ADD CONSTRAINT ctu_life_renamed_fk FOREIGN KEY(x,y) REFERENCES ctu_life_target.ctu_life_renamed(key_b,a) NOT VALID")
		hasFK = true
		check()
		flags := func(valid, deferrable, deferred bool) {
			c.run(c.conn, fixture.Select("SELECT convalidated,condeferrable,condeferred FROM pg_constraint WHERE conrelid='ctu_life_child_schema.ctu_life_child'::regclass::oid AND conname='ctu_life_renamed_fk'", []string{"convalidated", "condeferrable", "condeferred"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{valid, deferrable, deferred}}))
		}
		flags(false, false, false)
		c.exec("ALTER TABLE ctu_life_child_schema.ctu_life_child VALIDATE CONSTRAINT ctu_life_renamed_fk")
		flags(true, false, false)
		check()
		c.exec("ALTER TABLE ctu_life_child_schema.ctu_life_child ALTER CONSTRAINT ctu_life_renamed_fk DEFERRABLE INITIALLY DEFERRED")
		flags(true, true, true)
		check()
		c.exec("ALTER TABLE ctu_life_child_schema.ctu_life_child DROP CONSTRAINT ctu_life_renamed_fk", "ALTER TABLE ctu_life_child_two DROP CONSTRAINT ctu_life_fk")
		hasFK, hasSecond = false, false
		check()
		c.exec("ALTER TABLE ctu_life_target.ctu_life_renamed DROP CONSTRAINT ctu_life_renamed_u")
		hasUnique = false
		check()
		c.exec("ALTER TABLE ctu_life_target.ctu_life_renamed ADD CONSTRAINT ctu_life_renamed_u UNIQUE(a,key_b)")
		hasUnique = true
		check()
		key.Query = "SELECT column_name,ordinal_position FROM information_schema.key_column_usage WHERE table_name='ctu_life_renamed' AND constraint_name='ctu_life_renamed_u' ORDER BY ordinal_position"
		key.Rows = [][]any{{"a", int64(1)}, {"key_b", int64(2)}}
		c.run(c.conn, key)
		c.run(c.conn, fixture.Prepared(key))
		c.exec("ALTER TABLE ctu_life_child_two ADD CONSTRAINT ctu_life_fk FOREIGN KEY(x,y) REFERENCES ctu_life_target.ctu_life_renamed(a,key_b)")
		hasSecond = true
		check()
		c.exec("DROP TABLE ctu_life_child_two")
		hasSecond = false
		check()
		c.exec("DROP TABLE ctu_life_target.ctu_life_renamed")
		hasUnique = false
		check()
	})
	t.Run("duplicate_and_real_cascade", func(t *testing.T) {
		ctuScenario(t, func(c *ctuCase) {
			c.exec("CREATE TABLE ctu_cascade_parent(a integer)", "CREATE UNIQUE INDEX ctu_cascade_provider ON ctu_cascade_parent(a)",
				"CREATE TABLE ctu_cascade_child(a integer CONSTRAINT ctu_same REFERENCES ctu_cascade_parent(a))",
				"CREATE TABLE ctu_cascade_child_two(a integer CONSTRAINT ctu_same REFERENCES ctu_cascade_parent(a))",
				"CREATE TABLE ctu_cascade_other(a integer CONSTRAINT ctu_cascade_u UNIQUE)", "CREATE TABLE ctu_cascade_unrelated(a integer CONSTRAINT ctu_same REFERENCES ctu_cascade_other(a))")
			held := c.hold(c.conn, ctuQuery)
			unrelated := [][]any{c.pub("ctu_cascade_other", "ctu_cascade_u"), c.pub("ctu_cascade_other", "ctu_same")}
			row := c.pub("ctu_cascade_parent", "ctu_same")
			check := func(n int) {
				want := append([][]any(nil), unrelated...)
				for range n {
					want = append(want, row)
				}
				c.routes(held, "ctu_cascade_%", want...)
			}
			check(2)
			c.exec("ALTER TABLE ctu_cascade_child DROP CONSTRAINT ctu_same")
			check(1)
			c.exec("ALTER TABLE ctu_cascade_child ADD CONSTRAINT ctu_same FOREIGN KEY(a) REFERENCES ctu_cascade_parent(a)")
			check(2)
			c.exec("DROP INDEX ctu_cascade_provider CASCADE")
			check(0)
			c.exec("DROP TABLE ctu_cascade_other CASCADE")
			c.routes(held, "ctu_cascade_%")
		})
	})
}
