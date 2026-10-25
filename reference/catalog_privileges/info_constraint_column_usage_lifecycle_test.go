package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ccu_life_target", "ccu_life_child_schema")
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_life(a integer,dead text,b integer,CONSTRAINT ccu_life_u UNIQUE(b,a))", "ALTER TABLE ccu_life DROP COLUMN dead",
			"CREATE TABLE ccu_life_child(x integer CONSTRAINT ccu_life_nn NOT NULL,y integer,CONSTRAINT ccu_life_check CHECK(x>0 AND y>0),CONSTRAINT ccu_life_fk FOREIGN KEY(x,y) REFERENCES ccu_life(b,a))")
		held := c.hold(c.conn, ccuQuery)
		targetSchema, childSchema, table, column, unique, fk := "public", "public", "ccu_life", "b", "ccu_life_u", "ccu_life_fk"
		hasUnique, hasFK, hasCheck, hasNN := true, true, true, true
		bag := func() [][]any {
			var rows [][]any
			if hasUnique {
				for _, col := range []string{"a", column} {
					rows = append(rows, c.row(targetSchema, table, col, targetSchema, unique))
				}
			}
			if hasFK {
				for _, col := range []string{"a", column} {
					rows = append(rows, c.row(targetSchema, table, col, childSchema, fk))
				}
			}
			if hasCheck {
				for _, col := range []string{"x", "y"} {
					rows = append(rows, c.row(childSchema, "ccu_life_child", col, childSchema, "ccu_life_check"))
				}
			}
			if hasNN {
				rows = append(rows, c.row(childSchema, "ccu_life_child", "x", childSchema, "ccu_life_nn"))
			}
			return rows
		}
		check := func() { rows := bag(); c.check(c.conn, "ccu_life%", rows...); c.check(held, "ccu_life%", rows...) }
		check()
		if !c.b.PG {
			rows, err := c.conn.QueryContext(c.p.Ctx, ccuQuery, "ccu_life%")
			if err != nil {
				c.t.Fatal(err)
			}
			defer rows.Close()
			saved := ccuMetadata(c.t, rows, false)
			old := bag()
			c.exec("ALTER TABLE ccu_life RENAME COLUMN b TO key_b")
			ccuAssertRows(c.t, rows, old)
			ccuColumnTypes(c.t, saved, false)
		} else {
			c.exec("ALTER TABLE ccu_life RENAME COLUMN b TO key_b")
		}
		column = "key_b"
		check()
		c.exec("ALTER TABLE ccu_life RENAME CONSTRAINT ccu_life_u TO ccu_life_renamed_u", "ALTER TABLE ccu_life_child RENAME CONSTRAINT ccu_life_fk TO ccu_life_renamed_fk")
		unique, fk = "ccu_life_renamed_u", "ccu_life_renamed_fk"
		check()
		c.exec("ALTER TABLE ccu_life RENAME TO ccu_life_renamed")
		table = "ccu_life_renamed"
		check()
		c.exec("CREATE SCHEMA ccu_life_target", "CREATE SCHEMA ccu_life_child_schema", "ALTER TABLE ccu_life_renamed SET SCHEMA ccu_life_target")
		targetSchema = "ccu_life_target"
		check()
		c.exec("ALTER TABLE ccu_life_child SET SCHEMA ccu_life_child_schema")
		childSchema = "ccu_life_child_schema"
		check()
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child DROP CONSTRAINT ccu_life_renamed_fk")
		hasFK = false
		check()
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child ADD CONSTRAINT ccu_life_renamed_fk FOREIGN KEY(x,y) REFERENCES ccu_life_target.ccu_life_renamed(key_b,a) NOT VALID")
		hasFK = true
		check()
		flags := func(valid bool) {
			c.run(c.conn, fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='ccu_life_child_schema.ccu_life_child'::regclass::oid AND conname='ccu_life_renamed_fk'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{valid}}))
		}
		flags(false)
		// Recreate rather than extending the existing VALIDATE CONSTRAINT surface.
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child DROP CONSTRAINT ccu_life_renamed_fk", "ALTER TABLE ccu_life_child_schema.ccu_life_child ADD CONSTRAINT ccu_life_renamed_fk FOREIGN KEY(x,y) REFERENCES ccu_life_target.ccu_life_renamed(key_b,a)")
		flags(true)
		check()
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child DROP CONSTRAINT ccu_life_renamed_fk", "ALTER TABLE ccu_life_target.ccu_life_renamed DROP CONSTRAINT ccu_life_renamed_u")
		hasFK, hasUnique = false, false
		check()
		c.exec("ALTER TABLE ccu_life_target.ccu_life_renamed ADD CONSTRAINT ccu_life_renamed_u UNIQUE(a,key_b)")
		hasUnique = true
		check()
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child DROP CONSTRAINT ccu_life_check", "ALTER TABLE ccu_life_child_schema.ccu_life_child DROP CONSTRAINT ccu_life_nn")
		hasCheck, hasNN = false, false
		check()
		c.exec("ALTER TABLE ccu_life_child_schema.ccu_life_child ADD CONSTRAINT ccu_life_check CHECK(x>0 AND y>0)", "ALTER TABLE ccu_life_child_schema.ccu_life_child ADD CONSTRAINT ccu_life_nn NOT NULL x")
		hasCheck, hasNN = true, true
		check()
		c.exec("ALTER TABLE ccu_life_target.ccu_life_renamed DROP CONSTRAINT ccu_life_renamed_u", "ALTER TABLE ccu_life_target.ccu_life_renamed DROP COLUMN key_b")
		hasUnique = false
		check()

		c.exec("CREATE TABLE ccu_cascade_parent(a integer)", "CREATE UNIQUE INDEX ccu_cascade_provider ON ccu_cascade_parent(a)", "CREATE TABLE ccu_cascade_other(a integer CONSTRAINT ccu_cascade_u UNIQUE)", "CREATE TABLE ccu_cascade_child(a integer CONSTRAINT ccu_same REFERENCES ccu_cascade_parent(a))", "CREATE TABLE ccu_cascade_unrelated(a integer CONSTRAINT ccu_same REFERENCES ccu_cascade_other(a))")
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_cascade_parent VALUES(1)", 1))
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_cascade_child VALUES(1)", 1))
		unrelated := [][]any{c.pub("ccu_cascade_other", "a", "ccu_cascade_u"), c.pub("ccu_cascade_other", "a", "ccu_same")}
		before := append(append([][]any(nil), unrelated...), c.pub("ccu_cascade_parent", "a", "ccu_same"))
		c.check(c.conn, "ccu_cascade_%", before...)
		c.check(held, "ccu_cascade_%", before...)
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(c.t, c.p, c.b.PG, &notices)
		fixture.RunStep(c.t, c.p.Ctx, noticeConn, recorder, fixture.Exec("SET client_min_messages TO notice", 0))
		fixture.RunStep(c.t, c.p.Ctx, noticeConn, recorder, fixture.Exec("DROP INDEX ccu_cascade_provider CASCADE", 0))
		expected := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to constraint ccu_same on table ccu_cascade_child"}
		if c.b.PG {
			if len(notices) != 1 || notices[0] != expected {
				c.t.Fatalf("cascade notices: %+v want %+v", notices, expected)
			}
		} else if len(notices) != 0 {
			c.t.Fatalf("native index cascade notices: %+v want none", notices)
		}
		c.check(c.conn, "ccu_cascade_%", unrelated...)
		c.check(held, "ccu_cascade_%", unrelated...)
		c.run(c.conn, fixture.Select("SELECT a FROM ccu_cascade_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
