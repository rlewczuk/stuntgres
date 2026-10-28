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

func jsonpathArithmeticStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jpa_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$.a + $.b',fallback jsonb DEFAULT jsonb_path_query_array('1.20','$ + 2.300'),stored jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) VIRTUAL,CHECK(jsonb_path_query_array(doc,path) <> '[]'::jsonb))`, 0))
	run(fixture.Exec(`INSERT INTO jpa_stored(id,doc) VALUES(1,$1)`, 1, jsonpathArithmeticDocument))
	run(fixture.Exec(`CREATE VIEW jpa_view AS SELECT id,jsonb_path_query_array(path=>path,target=>doc) AS changed FROM jpa_stored`, 0))
	run(fixture.Exec(`CREATE INDEX jpa_expr ON jpa_stored(jsonb_path_query_array(doc,path))`, 0))
	check := fixture.Select(`SELECT s.id,s.fallback,s.stored,s.virtual,v.changed FROM jpa_stored s JOIN jpa_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "fallback", "stored", "virtual", "changed"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{{int64(1), `[3.500]`, `[3.500]`, `[3.500]`, `[3.500]`}})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	future := []fixture.Step{
		fixture.Exec(`INSERT INTO jpa_stored(id,doc) VALUES(2,'{"a":4.00,"b":0.25}')`, 1),
		fixture.Select(`SELECT fallback,stored,virtual FROM jpa_stored WHERE id=2`, []string{"fallback", "stored", "virtual"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{`[3.500]`, `[4.25]`, `[4.25]`}}),
		jsonpathNavigateSelect(`SELECT changed AS changed FROM jpa_view WHERE id=2`, `[4.25]`),
		fixture.Exec(`UPDATE jpa_stored SET path='$.a * $.b' WHERE id=2`, 1),
		fixture.Select(`SELECT id FROM jpa_stored WHERE jsonb_path_query_array(doc,path)='[1.0000]'::jsonb ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}),
	}
	// Explicitly native APIs; PostgreSQL exercises the same portable stored
	// recipes below and replays the native control phase's actual emitted SQL.
	if !b.PG {
		dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		unchanged := func() {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump, after.Bytes()) {
				t.Fatal("failed restore replaced arithmetic state")
			}
			run(check)
		}
		for _, failure := range []struct{ path, code, message string }{
			{`1 / 0`, "22012", `division by zero`},
			{`"x" + 1`, "22038", `left operand of jsonpath operator + is not a single numeric value`},
			{`1e131071 * 10`, "22003", `value overflows numeric format`},
		} {
			script := `CREATE TABLE jpa_bad(v jsonb); INSERT INTO jpa_bad VALUES(jsonb_path_query_array('1','$ + 1')); INSERT INTO jpa_bad VALUES(jsonb_path_query_array('1',` + fixture.JSONReferenceQuote(failure.path) + `));`
			d := fixture.ControlsReferenceDiagnostic(failure.code, failure.message)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			unchanged()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
		unchanged()
	}
	for _, s := range future {
		run(s)
	}
}

func TestReferenceJSONPathArithmeticControls(t *testing.T) {
	jsonpathNavigateScenario(t, jsonpathArithmeticStored)
}
