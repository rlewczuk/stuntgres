package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoKeyColumnUsageLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "kcu_life_schema")
	kcuScenario(t, func(c *kcuCase) {
		t := c.t
		c.exec("CREATE TABLE kcu_life(a integer,dead text,b integer,CONSTRAINT kcu_life_u UNIQUE(b,a))", "ALTER TABLE kcu_life DROP COLUMN dead")
		held := c.hold(c.conn, kcuQuery)
		check := func(schema, name, table, first, second string) {
			rows := [][]any{c.row(schema, name, table, first, 1, nil), c.row(schema, name, table, second, 2, nil)}
			c.check(c.conn, table, rows...)
			c.check(held, table, rows...)
		}
		check("public", "kcu_life_u", "kcu_life", "b", "a")
		if !c.b.PG {
			rows, err := c.conn.QueryContext(c.p.Ctx, kcuQuery, "kcu_life")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			kcuMetadata(t, rows, false)
			c.exec("ALTER TABLE kcu_life RENAME COLUMN b TO key_b")
			kcuAssertRows(t, rows, [][]any{c.pub("kcu_life_u", "kcu_life", "b", 1, nil), c.pub("kcu_life_u", "kcu_life", "a", 2, nil)})
		} else {
			c.exec("ALTER TABLE kcu_life RENAME COLUMN b TO key_b")
		}
		check("public", "kcu_life_u", "kcu_life", "key_b", "a")
		c.exec("ALTER TABLE kcu_life RENAME CONSTRAINT kcu_life_u TO kcu_new")
		check("public", "kcu_new", "kcu_life", "key_b", "a")
		c.exec("ALTER TABLE kcu_life RENAME TO kcu_renamed")
		check("public", "kcu_new", "kcu_renamed", "key_b", "a")
		c.exec("ALTER INDEX kcu_new RENAME TO kcu_life_index")
		check("public", "kcu_life_index", "kcu_renamed", "key_b", "a")
		c.exec("CREATE SCHEMA kcu_life_schema", "ALTER TABLE kcu_renamed SET SCHEMA kcu_life_schema")
		check("kcu_life_schema", "kcu_life_index", "kcu_renamed", "key_b", "a")
		c.exec("ALTER TABLE kcu_life_schema.kcu_renamed DROP CONSTRAINT kcu_life_index")
		c.check(c.conn, "kcu_renamed")
		c.check(held, "kcu_renamed")
		c.exec("ALTER TABLE kcu_life_schema.kcu_renamed ADD CONSTRAINT kcu_life_index UNIQUE(a,key_b)")
		check("kcu_life_schema", "kcu_life_index", "kcu_renamed", "a", "key_b")
		c.exec("CREATE TABLE kcu_life_child(x integer,y integer)", "ALTER TABLE kcu_life_child ADD CONSTRAINT kcu_life_fk FOREIGN KEY(x,y) REFERENCES kcu_life_schema.kcu_renamed(key_b,a) NOT VALID")
		fk := [][]any{c.pub("kcu_life_fk", "kcu_life_child", "x", 1, int64(2)), c.pub("kcu_life_fk", "kcu_life_child", "y", 2, int64(1))}
		c.check(c.conn, "kcu_life_child", fk...)
		c.check(held, "kcu_life_child", fk...)
		flags := func(valid bool) {
			t := c.t
			t.Helper()
			c.run(c.conn, fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='kcu_life_child'::regclass::oid AND conname='kcu_life_fk'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{valid}}))
		}
		flags(false)
		// VALIDATE CONSTRAINT is outside the existing native DDL surface.
		// Recreate through ordinary validated ADD instead; definition rows
		// remain identical despite the changed constraint identity/state.
		c.exec("ALTER TABLE kcu_life_child DROP CONSTRAINT kcu_life_fk", "ALTER TABLE kcu_life_child ADD CONSTRAINT kcu_life_fk FOREIGN KEY(x,y) REFERENCES kcu_life_schema.kcu_renamed(key_b,a)")
		flags(true)
		c.check(c.conn, "kcu_life_child", fk...)
		c.check(held, "kcu_life_child", fk...)
		c.exec("ALTER TABLE kcu_life_child DROP CONSTRAINT kcu_life_fk")
		c.check(c.conn, "kcu_life_child")
		c.check(held, "kcu_life_child")
		c.exec("DROP TABLE kcu_life_schema.kcu_renamed")
		c.check(c.conn, "kcu_renamed")
		c.check(held, "kcu_renamed")
		c.exec("CREATE TABLE kcu_cascade_parent(a integer)", "CREATE UNIQUE INDEX kcu_cascade_provider ON kcu_cascade_parent(a)", "CREATE TABLE kcu_cascade_other(a integer PRIMARY KEY)", "CREATE TABLE kcu_cascade_child(a integer CONSTRAINT kcu_same REFERENCES kcu_cascade_parent(a))", "CREATE TABLE kcu_cascade_unrelated(a integer CONSTRAINT kcu_same REFERENCES kcu_cascade_other(a))")
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_cascade_parent VALUES(1)", 1))
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_cascade_child VALUES(1)", 1))
		c.check(c.conn, "kcu_cascade_child", c.pub("kcu_same", "kcu_cascade_child", "a", 1, int64(1)))
		c.check(held, "kcu_cascade_child", c.pub("kcu_same", "kcu_cascade_child", "a", 1, int64(1)))
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, c.p, c.b.PG, &notices)
		fixture.RunStep(t, c.p.Ctx, noticeConn, recorder, fixture.Exec("SET client_min_messages TO notice", 0))
		fixture.RunStep(t, c.p.Ctx, noticeConn, recorder, fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}}))
		fixture.RunStep(t, c.p.Ctx, noticeConn, recorder, fixture.Exec("DROP INDEX kcu_cascade_provider CASCADE", 0))
		expected := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to constraint kcu_same on table kcu_cascade_child"}
		if c.b.PG {
			if len(notices) != 1 || notices[0] != expected {
				t.Fatalf("cascade notices: %+v want %+v", notices, expected)
			}
		} else if len(notices) != 0 {
			// Existing native index CASCADE retires dependent constraints,
			// but does not emit PostgreSQL's dependency notice.
			t.Fatalf("native index cascade notices: %+v want none", notices)
		}
		c.check(c.conn, "kcu_cascade_child")
		c.check(held, "kcu_cascade_child")
		c.check(c.conn, "kcu_cascade_unrelated", c.pub("kcu_same", "kcu_cascade_unrelated", "a", 1, int64(1)))
		c.check(held, "kcu_cascade_unrelated", c.pub("kcu_same", "kcu_cascade_unrelated", "a", 1, int64(1)))
		c.run(c.conn, fixture.Select("SELECT a FROM kcu_cascade_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
