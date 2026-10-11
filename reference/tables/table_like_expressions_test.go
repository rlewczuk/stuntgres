package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeDefaultsChecks(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT NOT NULL DEFAULT 7,dead TEXT,b INT DEFAULT 9,CONSTRAINT positive CHECK(a>0) NO INHERIT)", 0))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN dead", 0))
		run(fixture.Exec("INSERT INTO src VALUES(11,12)", 1))
		run(fixture.Exec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING DEFAULTS INCLUDING CONSTRAINTS,tail BOOL)", 0))
		run(fixture.Exec("INSERT INTO clone(lead,tail) VALUES('x',true)", 1))
		want := fixture.Select("SELECT * FROM clone", []string{"lead", "a", "b", "tail"}, []string{"TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(7), int64(9), true}})
		run(want)
		run(fixture.TableLikeError("INSERT INTO clone(lead,a,tail) VALUES('x',-1,true)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "clone" violates check constraint "positive"`, Detail: "Failing row contains (x, -1, 9, t).", Schema: "public", Table: "clone", Constraint: "positive"}))
		run(fixture.TableLikeError("INSERT INTO clone(lead,a,tail) VALUES('x',NULL,true)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (x, null, 9, t).", Schema: "public", Table: "clone", Column: "a"}))
		run(want)
		run(fixture.Select("SELECT * FROM src", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='clone'::regclass::oid AND contype='c'", []string{"conname"}, []string{"NAME"}, [][]any{{"positive"}}))
		if b.PG {
			run(fixture.Select("SELECT conname,connoinherit FROM pg_constraint WHERE conrelid='clone'::regclass AND contype='c'", []string{"conname", "connoinherit"}, []string{"NAME", "BOOL"}, [][]any{{"positive", true}}))
		}
		run(fixture.Exec("DROP TABLE src", 0))
		run(fixture.Exec("INSERT INTO clone(a) VALUES(2)", 1))
	})
}

func TestReferenceTableLikeGenerated(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(dead TEXT,b INT DEFAULT 42,c INT GENERATED ALWAYS AS(a*2) STORED,a INT CHECK(a>0))", 0))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN dead", 0))
		for i, option := range []string{"", "INCLUDING DEFAULTS", "INCLUDING GENERATED", "INCLUDING DEFAULTS INCLUDING GENERATED"} {
			name := fmt.Sprintf("copy%d", i)
			run(fixture.Exec("CREATE TABLE "+name+"(lead TEXT,LIKE src "+option+")", 0))
			run(fixture.Exec("INSERT INTO "+name+"(lead,a) VALUES('x',11)", 1))
			var def, gen any
			if i == 1 || i == 3 {
				def = int64(42)
			}
			if i >= 2 {
				gen = int64(22)
			}
			run(fixture.Select("SELECT * FROM "+name, []string{"lead", "b", "c", "a"}, []string{"TEXT", "INT4", "INT4", "INT4"}, [][]any{{"x", def, gen, int64(11)}}))
			if i >= 2 {
				run(fixture.TableLikeError("INSERT INTO "+name+"(a,c) VALUES(1,3)", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "c"`, Detail: `Column "c" is a generated column.`}))
				run(fixture.Exec("UPDATE "+name+" SET a=12", 1))
				run(fixture.Select("SELECT c FROM "+name, []string{"c"}, []string{"INT4"}, [][]any{{int64(24)}}))
			} else {
				run(fixture.Exec("INSERT INTO "+name+"(a,c) VALUES(-1,3)", 1))
			}
		}
	})
}

func TestReferenceTableLikeCookedReferences(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SEQUENCE s START 5", 0))
		run(fixture.Exec("CREATE TABLE anchor(a INT)", 0))
		run(fixture.Exec("CREATE TABLE src(a SMALLINT DEFAULT (32767::smallint+0::smallint),v VARCHAR(4) DEFAULT 'é',d DATE DEFAULT '2004-01-02',r REGCLASS DEFAULT 'anchor'::regclass,t REGTYPE DEFAULT 'int4'::regtype,n BIGINT DEFAULT nextval('s'))", 0))
		run(fixture.Exec("CREATE TABLE clone(LIKE src INCLUDING DEFAULTS)", 0))
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Exec("ALTER TABLE anchor RENAME TO renamed", 0))
		run(fixture.Exec("ALTER SEQUENCE s RENAME TO renamed_s", 0))
		run(fixture.Exec("DROP TABLE src", 0))
		run(fixture.Exec("INSERT INTO clone DEFAULT VALUES", 1))
		run(fixture.Select("SELECT a,v,d::text,r::text,t::text,n FROM clone", []string{"a", "v", "d", "r", "t", "n"}, []string{"INT2", "VARCHAR", "TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{int64(32767), "é", "2004-01-02", "renamed", "integer", int64(6)}}))
	})
}
