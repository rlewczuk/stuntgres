package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func numericMethodsStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE jpnm_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$[*].number()',fallback jsonb DEFAULT jsonb_path_query_array('[0,1,-2,-3.4,5.6]','$[*].abs()'),stored jsonb GENERATED ALWAYS AS(jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS(jsonb_path_query_array(doc,path)) VIRTUAL,CONSTRAINT jpnm_nonempty CHECK(jsonb_path_query_array(doc,path)<>'[]'::jsonb))`, 0),
		fixture.Exec(`INSERT INTO jpnm_stored(id,doc) VALUES(1,'["1.2300"]'),(2,NULL)`, 2),
		fixture.Exec(`INSERT INTO jpnm_stored(id,doc,path) VALUES(3,'[9007199254740993,"9007199254740993"]','$[*].double()'),(4,'[-1.25,1.25]','$[*].floor()'),(6,'[-1.25,1.25]','$[*].ceiling()')`, 3),
		fixture.Exec(`CREATE VIEW jpnm_view AS SELECT id,jsonb_path_query_array(path=>path,target=>doc) AS changed FROM jpnm_stored`, 0),
		fixture.Exec(`CREATE INDEX jpnm_expr ON jpnm_stored(jsonb_path_query_array(doc,path)) WHERE jsonb_path_query_array(doc,path) IS NOT NULL`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	check := fixture.Select(`SELECT s.id,s.fallback,s.stored,s.virtual,v.changed FROM jpnm_stored s JOIN jpnm_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "fallback", "stored", "virtual", "changed"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{
		{int64(1), `[0, 1, 2, 3.4, 5.6]`, `[1.2300]`, `[1.2300]`, `[1.2300]`},
		{int64(2), `[0, 1, 2, 3.4, 5.6]`, nil, nil, nil},
		{int64(3), `[0, 1, 2, 3.4, 5.6]`, `[9007199254740993, 9007199254740990]`, `[9007199254740993, 9007199254740990]`, `[9007199254740993, 9007199254740990]`},
		{int64(4), `[0, 1, 2, 3.4, 5.6]`, `[-2, 1]`, `[-2, 1]`, `[-2, 1]`},
		{int64(6), `[0, 1, 2, 3.4, 5.6]`, `[-1, 2]`, `[-1, 2]`, `[-1, 2]`},
	})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	return check
}

func TestReferenceJSONPathNumericMethodsStoredControls(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := numericMethodsStored(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpnm_stored(id,doc) VALUES(5,'["bad"]')`, fixture.ControlsReferenceDiagnostic("22036", numericMethodsInvalid("number", "bad"))))
		empty := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jpnm_stored" violates check constraint "jpnm_nonempty"`)
		empty.Detail = `Failing row contains (5, [], $[*].number(), [0, 1, 2, 3.4, 5.6], [], virtual).`
		empty.Schema, empty.Table, empty.Constraint = "public", "jpnm_stored", "jpnm_nonempty"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpnm_stored(id,doc) VALUES(5,'[]')`, empty))
		fixture.JSONPathNavigateBoth(t, p, b, c, check)
		future := []fixture.Step{
			fixture.Exec(`UPDATE jpnm_stored SET doc='["3.4560"]' WHERE id=1`, 1),
			jsonpathNavigateSelect(`SELECT changed FROM jpnm_view WHERE id=1`, `[3.4560]`),
			fixture.Exec(`INSERT INTO jpnm_stored(id,doc) VALUES(5,'[4.567]')`, 1),
			jsonpathNavigateSelect(`SELECT changed FROM jpnm_view WHERE id=5`, `[4.567]`),
		}
		dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			unchanged := func() {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump, after.Bytes()) {
					t.Fatal("failed Restore published catalog")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
			for _, tc := range []struct{ path, doc, code, message string }{
				{`$[*].number()`, `[1,"bad"]`, "22036", numericMethodsInvalid("number", "bad")},
				{`$[*].double()`, `[1,"NaN"]`, "22036", `NaN or Infinity is not allowed for jsonpath item method .double()`},
				{`$[*].abs()`, `[1,"2"]`, "22036", numericMethodsKind("abs")},
			} {
				script := `CREATE TABLE jpnm_bad(v jsonb); INSERT INTO jpnm_bad VALUES(jsonb_path_query_array('[1]','$[*].number()')); INSERT INTO jpnm_bad VALUES(jsonb_path_query_array(` + fixture.JSONReferenceQuote(tc.doc) + `,` + fixture.JSONReferenceQuote(tc.path) + `));`
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
				unchanged()
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
			unchanged()
			duringRead, cancelRead := context.WithCancel(p.Ctx)
			fixture.LexicalOrdinary(t, stuntgres.Restore(duringRead, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancelRead}), context.Canceled)
			cancelRead()
			unchanged()
		}
		for _, s := range future {
			// Replay spans native and PostgreSQL; attach backend-specific raw
			// metadata assertions only to this backend's own execution.
			if s.SelectRows {
				s = fixture.JSONPathNavigateViewMetadataStep(s, b.PG)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
