package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func soundexStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA sx_addon", 0))
	run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA sx_addon VERSION '1.2'", 0))
	run(fixture.Exec("CREATE TABLE sx_source(id int,name text,d text DEFAULT sx_addon.soundex('Ann'),score int DEFAULT sx_addon.difference('Ann','Anne'),g text GENERATED ALWAYS AS (sx_addon.soundex(name)) STORED,s int GENERATED ALWAYS AS (sx_addon.difference(name,'Ann')) STORED,CONSTRAINT sx_valid CHECK(sx_addon.difference(name,'Ann')>0))", 0))
	run(fixture.Exec("INSERT INTO sx_source(id,name) VALUES(1,'Ann'),(2,'Anne'),(3,'Andrew'),(4,NULL)", 4))
	run(fixture.Exec("CREATE VIEW sx_values AS SELECT id,d,score,g,s,sx_addon.soundex(name) AS code,sx_addon.difference(name,'Ann') AS agreement FROM sx_source", 0))
	run(fixture.Exec("CREATE VIEW sx_literals AS SELECT sx_addon.soundex('Ann') AS code,sx_addon.difference('Ann','Anne') AS agreement", 0))
	run(soundexLiteralView())
	run(fixture.Prepared(soundexLiteralView()))
	check := fixture.Select("SELECT id,d,score,g,s,code,agreement FROM sx_values ORDER BY id", []string{"id", "d", "score", "g", "s", "code", "agreement"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT", "INT4"}, [][]any{
		{int64(1), "A500", int64(4), "A500", int64(4), "A500", int64(4)},
		{int64(2), "A500", int64(4), "A500", int64(4), "A500", int64(4)},
		{int64(3), "A500", int64(4), "A536", int64(2), "A536", int64(2)},
		{int64(4), "A500", int64(4), nil, nil, nil, nil},
	})
	run(check)
	run(fixture.Prepared(check))
	return check
}

func soundexLiteralView() fixture.Step {
	return fixture.Select("SELECT code,agreement FROM sx_literals", []string{"code", "agreement"}, []string{"TEXT", "INT4"}, [][]any{{"A500", int64(4)}})
}

func TestReferenceSoundexStoredExpressions(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := soundexStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		failure := fixture.ImplicitError("INSERT INTO sx_source(id,name) VALUES(5,'Margaret')", fixture.Diagnostic{
			Code: "23514", Message: `new row for relation "sx_source" violates check constraint "sx_valid"`,
			Detail: "Failing row contains (5, Margaret, A500, 4, M626, 0).", Schema: "public", Table: "sx_source", Constraint: "sx_valid",
		})
		failure.ErrorStage = "execute"
		for _, prepared := range []bool{false, true} {
			failure.Prepared = prepared
			run(failure)
			run(check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id FROM sx_source WHERE sx_addon.soundex(name)='A500' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT sx_addon.soundex(name) AS code,count(*) FROM sx_source GROUP BY sx_addon.soundex(name) ORDER BY code NULLS LAST", []string{"code", "count"}, []string{"TEXT", "INT8"}, [][]any{{"A500", int64(2)}, {"A536", int64(1)}, {nil, int64(1)}}),
			fixture.Select("SELECT sx_addon.difference(name,'Ann') AS score,count(*) FROM sx_source GROUP BY sx_addon.difference(name,'Ann') ORDER BY score NULLS LAST", []string{"score", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(4), int64(2)}, {nil, int64(1)}}),
			fixture.Select("SELECT id,CASE WHEN id=3 THEN sx_addon.soundex(name) ELSE sx_addon.soundex('Ann') END AS code,sx_addon.difference(name,'Ann')::bigint AS score FROM sx_source WHERE id<=3 ORDER BY id", []string{"id", "code", "score"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "A500", int64(4)}, {int64(2), "A500", int64(4)}, {int64(3), "A536", int64(2)}}),
			fixture.Select("SELECT a.id,b.id FROM sx_source a JOIN sx_source b ON sx_addon.soundex(a.name)=sx_addon.soundex(b.name) WHERE a.id=1 ORDER BY b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		for _, name := range []string{"soundex", "difference"} {
			s := fixture.Select("SELECT count(*)>0 AS linked FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE d.refclassid=1255 AND nspname='sx_addon' AND p.proname='"+name+"'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
