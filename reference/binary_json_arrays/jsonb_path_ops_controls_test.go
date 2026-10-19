package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbPathOpsStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jpo_stored(id int PRIMARY KEY,doc jsonb,path jsonpath,fallback boolean DEFAULT ('false'::jsonb @? '$'::jsonpath),present boolean GENERATED ALWAYS AS (doc @? path) STORED,predicate boolean GENERATED ALWAYS AS (doc @@ path) VIRTUAL,CHECK(doc @? '$'::jsonpath))`, 0))
	run(fixture.Exec(`INSERT INTO jpo_stored(id,doc,path) VALUES(1,'false','$'),(2,'null','$'),(3,'[{"x":true},{}]','strict $[*].x')`, 3))
	run(fixture.Exec(`CREATE INDEX jpo_expression ON jpo_stored((doc @@ path))`, 0))
	run(fixture.Exec(`CREATE VIEW jpo_view AS SELECT id,doc @? path AS e,doc @@ path AS m FROM jpo_stored`, 0))
	check := fixture.Select(`SELECT s.id,s.fallback,s.present,s.predicate,v.e,v.m FROM jpo_stored s JOIN jpo_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "fallback", "present", "predicate", "e", "m"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, false, true, false}, {int64(2), true, true, nil, true, nil}, {int64(3), true, nil, true, nil, true}})
	jsonpathNavigateBoth(t, p, b, c, check)
	return check
}

func TestReferenceJSONBPathOpsStoredControls(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbPathOpsStored(t, p, b, c)
		future := []fixture.Step{
			fixture.Exec(`UPDATE jpo_stored SET doc='true',path='$' WHERE id=1`, 1),
			fixture.Select(`SELECT e,m FROM jpo_view WHERE id=1`, []string{"e", "m"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
			fixture.Exec(`INSERT INTO jpo_stored(id,doc,path) VALUES(4,'[]','$[*]')`, 1),
			fixture.Select(`SELECT fallback,present,predicate FROM jpo_stored WHERE id=4`, []string{"fallback", "present", "predicate"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, nil}}),
		}
		// Existing helper exercises same/fresh-engine Restore, retained cursors,
		// deterministic logical redump and actual emitted SQL on PostgreSQL.
		dump := jsonpathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			for _, tc := range []struct{ script, code, message, token, hint string }{
				{`CREATE TABLE jpo_bad(v boolean); INSERT INTO jpo_bad VALUES('true'::jsonb @? '$'); INSERT INTO jpo_bad VALUES('{}'::jsonb @@ '$[');`, "42601", `syntax error at end of jsonpath input`, `'$['`, ""},
				{`CREATE TABLE jpo_bad(v boolean); INSERT INTO jpo_bad VALUES('true'::jsonb @@ '$'); INSERT INTO jpo_bad VALUES('true'::text @? '$'::jsonpath);`, "42883", `operator does not exist: text @? jsonpath`, `@?`, `No operator matches the given name and argument types. You might need to add explicit type casts.`},
			} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				d.Position = fixture.Position(len([]rune(tc.script[:strings.LastIndex(tc.script, tc.token)])) + 1)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script)), &d)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump, after.Bytes()) {
					t.Fatal("failed operator Restore changed state")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
		}
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
