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

func integerDecimalStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE jpid_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$[*].decimal(6,2)',fallback jsonb DEFAULT jsonb_path_query_array('[1.5,-1.5]','$[*].integer()'),stored jsonb GENERATED ALWAYS AS(jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS(jsonb_path_query_array(doc,path)) VIRTUAL,CONSTRAINT jpid_nonempty CHECK(jsonb_path_query_array(doc,path)<>'[]'::jsonb))`, 0),
		fixture.Exec(`INSERT INTO jpid_stored(id,doc) VALUES(1,'[1.235,-2.345]'),(2,NULL)`, 2),
		fixture.Exec(`INSERT INTO jpid_stored(id,doc,path) VALUES(3,'[9223372036854775807,-9223372036854775808]','$[*].bigint()'),(4,'[1.5,-1.5]','$[*].integer()')`, 2),
		fixture.Exec(`CREATE VIEW jpid_view AS SELECT id,jsonb_path_query_array(path=>path,target=>doc) AS changed FROM jpid_stored`, 0),
		fixture.Exec(`CREATE INDEX jpid_expr ON jpid_stored(jsonb_path_query_array(doc,path)) WHERE jsonb_path_query_array(doc,path) IS NOT NULL`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	check := fixture.Select(`SELECT s.id,s.fallback,s.stored,s.virtual,v.changed FROM jpid_stored s JOIN jpid_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "fallback", "stored", "virtual", "changed"}, []string{"INT4", "JSONB", "JSONB", "JSONB", "JSONB"}, [][]any{
		{int64(1), `[2, -2]`, `[1.24, -2.35]`, `[1.24, -2.35]`, `[1.24, -2.35]`},
		{int64(2), `[2, -2]`, nil, nil, nil},
		{int64(3), `[2, -2]`, `[9223372036854775807, -9223372036854775808]`, `[9223372036854775807, -9223372036854775808]`, `[9223372036854775807, -9223372036854775808]`},
		{int64(4), `[2, -2]`, `[2, -2]`, `[2, -2]`, `[2, -2]`},
	})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	return check
}

func TestReferenceJSONPathIntegerDecimalStoredControls(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := integerDecimalStored(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpid_stored(id,doc) VALUES(5,'["bad"]')`, fixture.ControlsReferenceDiagnostic("22036", integerDecimalInvalid("decimal", "bad"))))
		empty := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jpid_stored" violates check constraint "jpid_nonempty"`)
		empty.Detail = `Failing row contains (5, [], $[*].decimal(6,2), [2, -2], [], virtual).`
		empty.Schema, empty.Table, empty.Constraint = "public", "jpid_stored", "jpid_nonempty"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpid_stored(id,doc) VALUES(5,'[]')`, empty))
		fixture.JSONPathNavigateBoth(t, p, b, c, check)
		future := []fixture.Step{
			fixture.Exec(`UPDATE jpid_stored SET doc='[3.456]' WHERE id=1`, 1),
			jsonpathNavigateSelect(`SELECT changed FROM jpid_view WHERE id=1`, `[3.46]`),
			fixture.Exec(`INSERT INTO jpid_stored(id,doc) VALUES(5,'[4.567]')`, 1),
			jsonpathNavigateSelect(`SELECT changed FROM jpid_view WHERE id=5`, `[4.57]`),
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
				{`$[*].integer()`, `[1,"bad"]`, "22036", integerDecimalInvalid("integer", "bad")},
				{`$[*].decimal(3,1)`, `[1,99.95]`, "22036", integerDecimalInvalid("decimal", "99.95")},
				{`$[*].decimal(0)`, `[1]`, "22023", `NUMERIC precision 0 must be between 1 and 1000`},
			} {
				script := `CREATE TABLE jpid_bad(v jsonb); INSERT INTO jpid_bad VALUES(jsonb_path_query_array('[1]','$[*].integer()')); INSERT INTO jpid_bad VALUES(jsonb_path_query_array(` + fixture.JSONReferenceQuote(tc.doc) + `,` + fixture.JSONReferenceQuote(tc.path) + `));`
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
