package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathQueryTZStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
	run(fixture.Exec(`CREATE TABLE jpqtz_stored(id int PRIMARY KEY,doc jsonb DEFAULT jsonb_path_query_first_tz('"2023-08-15"','$.timestamp_tz()'),CONSTRAINT jpqtz_present CHECK(jsonb_path_exists_tz(doc,'$')))`, 0))
	run(fixture.Exec(`INSERT INTO jpqtz_stored VALUES(1,'"2015-08-01 12:00:00-05"')`, 1))
	args := `(doc,'` + jsonpathQueryTZPath + `','` + jsonpathQueryTZVars + `')`
	run(fixture.Exec(`CREATE VIEW jpqtz_scalar AS SELECT id,jsonb_path_exists_tz`+args+` AS present,jsonb_path_match_tz(doc,'`+jsonpathQueryTZBoolean+`','`+jsonpathQueryTZVars+`') AS predicate,jsonb_path_query_first_tz(path=>'`+jsonpathQueryTZPath+`',target=>doc,vars=>'`+jsonpathQueryTZVars+`') AS first,jsonb_path_query_array_tz`+args+` AS items FROM jpqtz_stored`, 0))
	run(fixture.Exec(`CREATE VIEW jpqtz_target AS SELECT id,jsonb_path_query_tz`+args+` AS v FROM jpqtz_stored`, 0))
	run(fixture.Exec(`CREATE VIEW jpqtz_source AS SELECT id,q.v,q.n FROM jpqtz_stored,jsonb_path_query_tz`+args+` WITH ORDINALITY AS q(v,n)`, 0))
	check := fixture.Select(`SELECT id,present,predicate,first,items FROM jpqtz_scalar ORDER BY id`, []string{"id", "present", "predicate", "first", "items"}, []string{"INT4", "BOOL", "BOOL", "JSONB", "JSONB"}, [][]any{{int64(1), true, true, jsonpathQueryTZDocument, `["2015-08-01 12:00:00-05"]`}})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	return check
}

func jsonpathQueryTZFuture() []fixture.Step {
	return []fixture.Step{
		fixture.Exec(`SET TIME ZONE 'Asia/Tokyo'`, 0),
		fixture.Select(`SELECT id,present,predicate,first,items FROM jpqtz_scalar WHERE id=1`, []string{"id", "present", "predicate", "first", "items"}, []string{"INT4", "BOOL", "BOOL", "JSONB", "JSONB"}, [][]any{{int64(1), false, false, nil, `[]`}}),
		fixture.Select(`SELECT v FROM jpqtz_target WHERE id=1`, []string{"v"}, []string{"JSONB"}, nil),
		fixture.Select(`SELECT v,n FROM jpqtz_source WHERE id=1`, []string{"v", "n"}, []string{"JSONB", "INT8"}, nil),
		fixture.Exec(`INSERT INTO jpqtz_stored(id) VALUES(2)`, 1),
		fixture.Select(`SELECT doc FROM jpqtz_stored WHERE id=2`, []string{"doc"}, []string{"JSONB"}, jsonpathQueryRows(`"2023-08-15T00:00:00+09:00"`)),
	}
}

func TestReferenceJSONPathQueryTZControls(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathQueryTZStored(t, p, b, c)
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT v,n FROM jpqtz_source ORDER BY id,n`, []string{"v", "n"}, []string{"JSONB", "INT8"}, [][]any{{jsonpathQueryTZDocument, int64(1)}}))
		future := jsonpathQueryTZFuture()
		dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			unchanged := func() {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after.Bytes(), dump) {
					t.Fatal("failed/canceled Restore changed durable state")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
			for _, f := range jsonpathQueryTZFunctions {
				if f.set {
					continue
				}
				for slot := range 4 {
					args := []string{`'true'`, `'$'`, `'{}'`, `false`}
					args[slot] = "$1"
					script := `/*é🙂*/ CREATE TABLE jpqtz_bad(v text); INSERT INTO jpqtz_bad VALUES(` + f.name + `(` + strings.Join(args, ",") + `)::text);`
					d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
					d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
					unchanged()
				}
			}
			script := `CREATE TABLE jpqtz_bad(v jsonb); INSERT INTO jpqtz_bad VALUES(jsonb_path_query_first_tz('"2023-08-15"','$.timestamp_tz()')); INSERT INTO jpqtz_bad VALUES(jsonb_path_query_first_tz('"bad"','$.datetime()'));`
			d := fixture.ControlsReferenceDiagnostic("22031", `datetime format is not recognized: "bad"`)
			d.Hint = `Use a datetime template argument to specify the input data format.`
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			unchanged()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
			unchanged()
			ctx, cancel = context.WithCancel(p.Ctx)
			fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}), context.Canceled)
			cancel()
			unchanged()
		}
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
