package reference_test

import (
	"bytes"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesLifecycle(t *testing.T) {
	itScenario(t, func(c *itCase) {
		held := c.hold(itSelect)
		check := func(schema, name string, want ...[]any) {
			s := itStep(c.b.PG, itSelect, want, schema, name)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		check("it_scope", "later")
		c.exec("CREATE TABLE it_scope.later(a integer)")
		check("it_scope", "later", c.row("later", "BASE TABLE", "YES"))
		c.exec("ALTER TABLE it_scope.later RENAME TO renamed")
		check("it_scope", "later")
		check("it_scope", "renamed", c.row("renamed", "BASE TABLE", "YES"))
		c.exec("CREATE SCHEMA it_moved", "ALTER TABLE it_scope.renamed SET SCHEMA it_moved")
		check("it_scope", "renamed")
		check("it_moved", "renamed", itRow(c.database, "it_moved", "renamed", "BASE TABLE", "YES"))
		c.exec("DROP TABLE it_moved.renamed")
		check("it_moved", "renamed")
		c.exec("CREATE VIEW it_scope.later AS SELECT 1 AS n")
		check("it_scope", "later", c.row("later", "VIEW", "NO"))
		c.exec("DROP VIEW it_scope.later")
		check("it_scope", "later")
		c.exec("CREATE TABLE it_scope.sentinel(a integer)", "CREATE TABLE it_scope.target(a integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO it_scope.sentinel VALUES(7)", 1))
		for _, tc := range []struct{ q, code, message string }{{"CREATE TABLE it_scope.sentinel(a integer)", "42P07", `relation "sentinel" already exists`}, {"ALTER TABLE it_scope.sentinel RENAME TO target", "42P07", `relation "target" already exists`}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(c.conn, s)
				c.bag(c.row("sentinel", "BASE TABLE", "YES"), c.row("target", "BASE TABLE", "YES"))
				c.run(c.conn, fixture.Select("SELECT a FROM it_scope.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			c.exec("BEGIN", "CREATE TABLE it_scope.tx(a integer)")
			check("it_scope", "tx", c.row("tx", "BASE TABLE", "YES"))
			c.run(peer, itStep(c.b.PG, itSelect, nil, "it_scope", "tx"))
			c.exec(end)
			var want [][]any
			if end == "COMMIT" {
				want = [][]any{c.row("tx", "BASE TABLE", "YES")}
			}
			check("it_scope", "tx", want...)
			c.run(peer, itStep(c.b.PG, itSelect, want, "it_scope", "tx"))
		}
		c.exec("BEGIN", "DROP TABLE it_scope.tx")
		check("it_scope", "tx")
		c.exec("ROLLBACK")
		check("it_scope", "tx", c.row("tx", "BASE TABLE", "YES"))
		c.exec("BEGIN", "DROP TABLE it_scope.tx", "COMMIT")
		check("it_scope", "tx")
	})
}

func TestReferenceInfoTablesNativeControls(t *testing.T) {
	itScenario(t, func(c *itCase) {
		c.exec("CREATE TABLE it_scope.base(a integer)", "CREATE VIEW it_scope.simple AS SELECT a FROM it_scope.base", "CREATE VIEW it_scope.constant AS SELECT 1 AS n", "CREATE TYPE it_scope.row_t AS(a integer)", "CREATE TABLE it_scope.typed OF it_scope.row_t")
		typed := c.row("typed", "BASE TABLE", "YES")
		typed[6], typed[7], typed[8], typed[10] = c.database, "it_scope", "row_t", "YES"
		want := [][]any{c.row("base", "BASE TABLE", "YES"), c.row("simple", "VIEW", "YES"), c.row("constant", "VIEW", "NO"), typed}
		c.bag(want...)
		if c.b.PG {
			return
		} // Reset/Dump/Restore are public native controls, not SQL.
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			c.t.Fatal(err)
		}
		q := "SELECT * FROM information_schema.tables WHERE table_schema='it_scope' ORDER BY table_name"
		old, err := c.conn.QueryContext(c.p.Ctx, q)
		if err != nil {
			c.t.Fatal(err)
		}
		defer old.Close()
		oldTypes, err := old.ColumnTypes()
		if err != nil {
			c.t.Fatal(err)
		}
		c.exec("ALTER TABLE it_scope.base RENAME TO renamed")
		c.check("base")
		c.check("renamed", c.row("renamed", "BASE TABLE", "YES"))
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			c.t.Fatal(err)
		}
		c.bag()
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		c.bag(want...)
		freshTypes, err := old.ColumnTypes()
		if err != nil {
			c.t.Fatal(err)
		}
		for i := range oldTypes {
			if oldTypes[i].Name() != freshTypes[i].Name() || oldTypes[i].DatabaseTypeName() != freshTypes[i].DatabaseTypeName() {
				c.t.Fatal("retained type changed")
			}
		}
		itMetadata(c.t, old, false)
		ordered := [][]any{want[0], want[2], want[1], want[3]}
		var got [][]any
		for old.Next() {
			row := make([]any, 12)
			dst := make([]any, 12)
			for i := range row {
				dst[i] = &row[i]
			}
			if err := old.Scan(dst...); err != nil {
				c.t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := old.Err(); err != nil {
			c.t.Fatal(err)
		}
		if !reflect.DeepEqual(got, ordered) {
			c.t.Fatalf("retained rows=%v want %v", got, ordered)
		}
	})
}
