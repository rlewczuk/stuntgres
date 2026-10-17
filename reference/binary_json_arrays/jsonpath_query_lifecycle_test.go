package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathQueryStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jpq_stored(id int PRIMARY KEY,doc jsonb,fallback jsonb DEFAULT jsonb_path_query_first('[2,3]','$[*]'),present boolean GENERATED ALWAYS AS (jsonb_path_exists(doc,'$.a[*]')) STORED,predicate boolean GENERATED ALWAYS AS (jsonb_path_match(doc,'exists($.a[*])')) VIRTUAL,CONSTRAINT jpq_has CHECK(jsonb_path_exists(doc,'$.a[*]')))`, 0))
	run(fixture.Exec(`INSERT INTO jpq_stored(id,doc) VALUES(1,'{"a":[2,3,4]}')`, 1))
	run(fixture.Exec(`CREATE INDEX jpq_first ON jpq_stored(jsonb_path_query_first(doc,'$.a[*]'))`, 0))
	run(fixture.Exec(`CREATE VIEW jpq_scalar AS SELECT id,jsonb_path_query_first(path=>'$.a[*]',target=>doc) AS first FROM jpq_stored`, 0))
	run(fixture.Exec(`CREATE VIEW jpq_target AS SELECT id,jsonb_path_query(doc,'$.a[*]') AS v FROM jpq_stored`, 0))
	run(fixture.Exec(`CREATE VIEW jpq_source AS SELECT id,q.v,q.n FROM jpq_stored,jsonb_path_query(doc,'$.a[*]') WITH ORDINALITY AS q(v,n)`, 0))
	check := fixture.Select(`SELECT s.id,s.fallback,s.present,s.predicate,v.first,q.v,q.n FROM jpq_stored s JOIN jpq_scalar v ON s.id=v.id JOIN jpq_source q ON q.id=s.id ORDER BY q.n`, []string{"id", "fallback", "present", "predicate", "first", "v", "n"}, []string{"INT4", "JSONB", "BOOL", "BOOL", "JSONB", "JSONB", "INT8"}, [][]any{{int64(1), `2`, true, true, `2`, `2`, int64(1)}, {int64(1), `2`, true, true, `2`, `3`, int64(2)}, {int64(1), `2`, true, true, `2`, `4`, int64(3)}})
	jsonpathNavigateBoth(t, p, b, c, check)
	return check
}

func TestReferenceJSONPathQueryConsumers(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathQueryStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		jsonpathNavigateBoth(t, p, b, c, fixture.Select(`SELECT v FROM jpq_target ORDER BY v`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`2`, `3`, `4`)))
		jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT CASE WHEN present THEN COALESCE(jsonb_path_query_first(doc,'$.a[*]'),'0'::jsonb) ELSE '9'::jsonb END AS changed FROM jpq_stored`, "JSONB", `2`))
		jsonpathNavigateBoth(t, p, b, c, fixture.Select(`SELECT jsonb_path_exists(doc,'$.a[*]') AS changed,count(*) AS n FROM jpq_stored GROUP BY jsonb_path_exists(doc,'$.a[*]')`, []string{"changed", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}}))
		run(fixture.Exec(`CREATE TABLE jpq_copy AS SELECT v FROM jpq_target`, 3))
		run(fixture.Exec(`INSERT INTO jpq_copy SELECT v FROM jpq_source WHERE n=1`, 1))
		run(fixture.Select(`SELECT v FROM jpq_copy ORDER BY v`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`2`, `2`, `3`, `4`)))
		run(fixture.Exec(`ALTER TABLE jpq_stored RENAME COLUMN doc TO payload`, 0))
		jsonpathNavigateBoth(t, p, b, c, check)
	})
}

func TestReferenceJSONPathQueryControls(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathQueryStored(t, p, b, c)
		future := []fixture.Step{fixture.Exec(`UPDATE jpq_stored SET doc='{"a":[7]}' WHERE id=1`, 1), fixture.Select(`SELECT v FROM jpq_target`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`7`))}
		dump := jsonpathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			unchanged := func() {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after.Bytes(), dump) {
					t.Fatal("failed Restore changed persistent state")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
			for _, name := range []string{"jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first"} {
				for slot := range 4 {
					args := []string{`'true'`, `'$'`, `'{}'`, `false`}
					args[slot] = "$1"
					script := `/*é🙂*/ CREATE TABLE jpq_bad(v text); INSERT INTO jpq_bad VALUES(` + name + `(` + strings.Join(args, ",") + `)::text);`
					d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
					d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
					unchanged()
				}
			}
		}
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceJSONPathQueryAtomicity(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpq_input(id int,doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpq_input VALUES(1,'[{"x":2}]'),(2,'[{"x":3},{}]')`, 2))
		run(fixture.Exec(`CREATE TABLE jpq_out(v jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpq_out VALUES('99')`, 1))
		for _, q := range []string{`INSERT INTO jpq_out SELECT jsonb_path_query(doc,'strict $[*].x') FROM jpq_input ORDER BY id`, `INSERT INTO jpq_out SELECT q.v FROM jpq_input,jsonb_path_query(doc,'strict $[*].x') AS q(v) ORDER BY id`, `CREATE TABLE jpq_failed AS SELECT jsonb_path_query(doc,'strict $[*].x') AS v FROM jpq_input ORDER BY id`} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`))
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select(`SELECT v FROM jpq_out`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`99`)))
		}
		run(fixture.Select(`SELECT count(*) AS absent FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='jpq_failed'`, []string{"absent"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec(`INSERT INTO jpq_out SELECT jsonb_path_query(doc,'strict $[*].x','{}',true) FROM jpq_input ORDER BY id`, 2))
		run(fixture.Select(`SELECT v FROM jpq_out ORDER BY v`, []string{"v"}, []string{"JSONB"}, jsonpathQueryRows(`2`, `3`, `99`)))
	})
}
