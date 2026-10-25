package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableCreate(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair (id WITH OPTIONS DEFAULT 7 NOT NULL,note DEFAULT 'ok',CONSTRAINT positive CHECK(id>0))", 0))
		run(fixture.Exec("CREATE TABLE plain OF pair (id DEFAULT 7,note WITH OPTIONS DEFAULT 'ok')", 0))
		run(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, int64(11), []byte("hey")))
		run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
		run(typedTablePairRows("t", [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, []bool{false, true}, 2)
		rows, err := c.QueryContext(p.Ctx, "SELECT * FROM t")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if length, known := columns[1].Length(); !known || length != 5 {
			t.Errorf("varchar length=(%d,%t), want (5,true)", length, known)
		}
		nn := typedTableError("23502", `null value in column "id" of relation "t" violates not-null constraint`)
		nn.Detail, nn.Schema, nn.Table, nn.Column = "Failing row contains (null, ok).", "public", "t", "id"
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO t(id) VALUES(NULL)", "execute", nn)
		check := typedTableError("23514", `new row for relation "t" violates check constraint "positive"`)
		check.Detail, check.Schema, check.Table, check.Constraint = "Failing row contains (-1, ok).", "public", "t", "positive"
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO t(id) VALUES(-1)", "execute", check)
		run(typedTablePairRows("t", [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
		run(fixture.Exec("INSERT INTO plain VALUES(NULL,NULL),(-1,'free')", 2))
		run(fixture.Exec("CREATE TABLE holder(v pair)", 0))
		run(fixture.Exec("INSERT INTO holder VALUES('(,free)'),('(-1,free)')", 2))
		run(fixture.TypedTableAttachment("t", "pair", true))
		run(fixture.Select("SELECT 't'::regtype::oid<>'pair'::regtype::oid AS distinct_row_type", []string{"distinct_row_type"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='t'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='pair'::regtype::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
	})
}

func TestReferenceTypedTableEmptyQuotedAndKeys(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE empty_pair AS ()", 0))
		run(fixture.Exec("CREATE TABLE empty_t OF empty_pair", 0))
		run(fixture.Exec("INSERT INTO empty_t DEFAULT VALUES", 1))
		run(fixture.Select("SELECT count(*) AS n FROM empty_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.TypedTableAttachment("empty_t", "empty_pair", true))
		run(fixture.Exec(`CREATE TYPE public."Pé" AS ("Id" integer,"a""b" text)`, 0))
		run(fixture.Exec(`CREATE TABLE public."Té" OF public."Pé" ("Id" WITH OPTIONS PRIMARY KEY,"a""b" UNIQUE)`, 0))
		run(fixture.Exec(`INSERT INTO "Té" VALUES($1,$2)`, 1, int64(11), "O'Brien é"))
		run(fixture.Select(`SELECT * FROM "Té"`, []string{"Id", `a"b`}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "O'Brien é"}}))
		d := typedTableError("23505", `duplicate key value violates unique constraint "Té_pkey"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Key (\"Id\")=(11) already exists.", "public", "Té", "Té_pkey"
		fixture.CompositeReferenceError(t, p, c, `INSERT INTO "Té" VALUES(11,'other')`, "execute", d)
	})
}
