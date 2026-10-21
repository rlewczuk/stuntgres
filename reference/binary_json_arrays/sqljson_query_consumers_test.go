package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sqljsonQueryStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE sqjq_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$.a[*]',needle int DEFAULT JSON_VALUE('{"x":2}','$.x' RETURNING int),present boolean GENERATED ALWAYS AS (JSON_EXISTS(doc,'$.a[*]')) STORED,scalar int GENERATED ALWAYS AS (JSON_VALUE(doc,'$.s' RETURNING int DEFAULT -1 ON EMPTY DEFAULT -2 ON ERROR)) VIRTUAL,CHECK(JSON_EXISTS(doc,'$.a[*]')))`, 0))
	run(fixture.Exec(`INSERT INTO sqjq_stored(id,doc) VALUES(1,'{"a":[1,2],"s":"123"}')`, 1))
	run(fixture.Exec(`CREATE INDEX sqjq_idx ON sqjq_stored(JSON_VALUE(doc,'$.s' RETURNING int DEFAULT -1 ON EMPTY DEFAULT -2 ON ERROR))`, 0))
	run(fixture.Exec(`CREATE VIEW sqjq_view AS SELECT id,JSON_EXISTS(doc,path PASSING needle AS x) AS found,JSON_QUERY(doc,path PASSING needle AS x WITH WRAPPER KEEP QUOTES EMPTY ARRAY ON EMPTY EMPTY OBJECT ON ERROR) AS items,JSON_VALUE(doc,'$.s' RETURNING int DEFAULT -1 ON EMPTY DEFAULT -2 ON ERROR) AS value FROM sqjq_stored`, 0))
	check := fixture.Select(`SELECT s.id,s.needle,s.present,s.scalar,v.found,v.items,v.value FROM sqjq_stored s JOIN sqjq_view v ON v.id=s.id ORDER BY s.id`, []string{"id", "needle", "present", "scalar", "found", "items", "value"}, []string{"INT4", "INT4", "BOOL", "INT4", "BOOL", "JSONB", "INT4"}, [][]any{{int64(1), int64(2), true, int64(123), true, `[1, 2]`, int64(123)}})
	sqljsonBoth(t, p, b, c, check)
	return check
}

func TestReferenceSQLJSONQueryConsumers(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonQueryStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE sqjq_copy AS SELECT JSON_VALUE(doc,'$.s' RETURNING int) AS v FROM sqjq_stored WHERE JSON_EXISTS(doc,'$.a[*]')`, 1))
		sqljsonBoth(t, p, b, c, sqljsonQueryOne(`SELECT v FROM sqjq_copy`, "INT4", int64(123)))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT JSON_EXISTS(doc,'$.a[*]') AS v,count(*) AS n FROM sqjq_stored GROUP BY JSON_EXISTS(doc,'$.a[*]')`, []string{"v", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}}))
		run(fixture.Exec(`UPDATE sqjq_copy SET v=JSON_VALUE('{"x":"7"}','$.x' RETURNING int)`, 1))
		sqljsonBoth(t, p, b, c, sqljsonQueryOne(`SELECT v FROM sqjq_copy`, "INT4", int64(7)))
		for _, rename := range []string{`ALTER TABLE sqjq_stored RENAME COLUMN doc TO payload`, `ALTER TABLE sqjq_stored RENAME COLUMN path TO route`, `ALTER TABLE sqjq_stored RENAME COLUMN needle TO binding`} {
			run(fixture.Exec(rename, 0))
			// Only the explicit source-column name in this query changes; the view
			// and its captured children remain valid after every dependency rename.
			sqljsonBoth(t, p, b, c, fixture.Select(`SELECT id,found,items,value FROM sqjq_view`, []string{"id", "found", "items", "value"}, []string{"INT4", "BOOL", "JSONB", "INT4"}, [][]any{{int64(1), true, `[1, 2]`, int64(123)}}))
		}
		for _, tc := range []struct{ q, code, message string }{
			{`CREATE INDEX sqjq_dynamic ON sqjq_stored(JSON_VALUE(payload,route))`, "42P17", `functions in index expression must be marked IMMUTABLE`},
			{`CREATE TABLE sqjq_dynamic_generated(d jsonb,p jsonpath,v text GENERATED ALWAYS AS (JSON_VALUE(d,p)) STORED)`, "42P17", `generation expression is not immutable`},
			{`CREATE TABLE sqjq_mixed_generated(d date,z timestamptz,v boolean GENERATED ALWAYS AS (JSON_VALUE('{}','$a == $b' PASSING d AS a,z AS b RETURNING boolean)) STORED)`, "42P17", `generation expression is not immutable`},
			{`CREATE TABLE sqjq_null_mixed(v boolean GENERATED ALWAYS AS (JSON_VALUE('{}','$a == $b' PASSING NULL::date AS a,NULL::timestamptz AS b RETURNING boolean)) STORED)`, "42P17", `generation expression is not immutable`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				s := fixture.ErrorDiagnostic(tc.q, d)
				run(s)
				s.Prepared, s.ErrorStage = true, "execute"
				run(s)
			})
		}
		// Bare temporal transport and constant NULL paths are not blanket STABLE.
		run(fixture.Exec(`CREATE TABLE sqjq_properties(d date,tm time,tz timetz,ts timestamp,z timestamptz,p jsonpath,v text GENERATED ALWAYS AS (JSON_VALUE('{}','$v' PASSING d AS v)) STORED,n text GENERATED ALWAYS AS (JSON_VALUE('{}',NULL::jsonpath PASSING d AS v)) STORED)`, 0))
		for _, column := range []string{"d", "tm", "tz", "ts", "z"} {
			run(fixture.Exec(`CREATE INDEX sqjq_temporal_bare_`+column+` ON sqjq_properties(JSON_VALUE('{}','$v' PASSING `+column+` AS v))`, 0))
		}
		run(fixture.Exec(`CREATE INDEX sqjq_same_zone ON sqjq_properties(JSON_VALUE('{}','$a == $b' PASSING tz AS a,z AS b RETURNING boolean))`, 0))
		run(fixture.Exec(`INSERT INTO sqjq_properties(d) VALUES('2018-02-21')`, 1))
		sqljsonBoth(t, p, b, c, fixture.Select(`SELECT v,n FROM sqjq_properties`, []string{"v", "n"}, []string{"TEXT", "TEXT"}, [][]any{{`2018-02-21`, nil}}))
		// A genuine outer conversion is not caught by JSON_VALUE's ON ERROR.
		t.Run("outer cast", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "aaa"`)
			sqljsonDiagnostic(t, p, b, c, `SELECT JSON_VALUE($1::jsonb,'$' DEFAULT '7' ON ERROR)::int AS v`, d, false, `"aaa"`)
		})
		for _, tc := range []struct{ q, token, message string }{
			{`SELECT JSON_VALUE('{}','$' DEFAULT v ON EMPTY) FROM (VALUES(1)) t(v)`, `v ON EMPTY`, `can only specify a constant, non-aggregate function, or operator expression for DEFAULT`},
			{`SELECT JSON_VALUE('{}','$' DEFAULT row_number() OVER () ON EMPTY)`, `row_number`, `can only specify a constant, non-aggregate function, or operator expression for DEFAULT`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				// Exact positions/stages are oracle discovery obligations, not a
				// relaxation of the shared complete diagnostic assertion.
				d := fixture.ControlsReferenceDiagnostic("42804", tc.message)
				d.Position = fixture.Position(len([]rune(tc.q[:strings.Index(tc.q, tc.token)])) + 1)
				sqljsonDiagnostic(t, p, b, c, tc.q, d, true)
			})
		}
		t.Run("default dependency", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE SEQUENCE sqjq_fallback`, 0))
			run(fixture.Exec(`CREATE VIEW sqjq_default_view AS SELECT JSON_VALUE('{}','$.x' RETURNING bigint DEFAULT nextval('sqjq_fallback') ON EMPTY DEFAULT 0 ON ERROR) AS v`, 0))
			run(sqljsonQueryOne(`SELECT v FROM sqjq_default_view`, "INT8", int64(1)))
			run(fixture.Prepared(sqljsonQueryOne(`SELECT v FROM sqjq_default_view`, "INT8", int64(2))))
			run(fixture.Exec(`ALTER SEQUENCE sqjq_fallback RENAME TO sqjq_fallback_renamed`, 0))
			run(sqljsonQueryOne(`SELECT v FROM sqjq_default_view`, "INT8", int64(3)))
			run(fixture.Prepared(sqljsonQueryOne(`SELECT v FROM sqjq_default_view`, "INT8", int64(4))))
			d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence sqjq_fallback_renamed because other objects depend on it")
			d.Detail = "view sqjq_default_view depends on sequence sqjq_fallback_renamed"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic(`DROP SEQUENCE sqjq_fallback_renamed`, d))
		})
	})
}
