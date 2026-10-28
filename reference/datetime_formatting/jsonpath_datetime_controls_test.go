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

func jsonpathDatetimeStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE TABLE jpdt_stored(id int PRIMARY KEY,doc jsonb,path jsonpath DEFAULT '$[*].date()',stored jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) STORED,virtual jsonb GENERATED ALWAYS AS (jsonb_path_query_array(doc,path)) VIRTUAL,CONSTRAINT jpdt_nonempty CHECK(jsonb_path_query_array(doc,path)<>'[]'::jsonb))`, 0),
		fixture.Exec(`INSERT INTO jpdt_stored(id,doc) VALUES(1,'["2023-08-15"]')`, 1),
		fixture.Exec(`CREATE VIEW jpdt_view AS SELECT id,jsonb_path_query_array(path=>path,target=>doc) AS changed FROM jpdt_stored`, 0),
		fixture.Exec(`CREATE INDEX jpdt_expr ON jpdt_stored(jsonb_path_query_array(doc,path))`, 0),
		fixture.Exec(`CREATE TABLE jpdt_recipes(id int,p jsonpath DEFAULT '$.datetime("DD.MM.YYYY")',v jsonb DEFAULT jsonb_path_query_array('"12:34:56.789"','$.time(2)'))`, 0),
		fixture.Exec(`INSERT INTO jpdt_recipes(id) VALUES(1)`, 1),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	check := fixture.Select(`SELECT s.id,s.stored,s.virtual,v.changed FROM jpdt_stored s JOIN jpdt_view v ON s.id=v.id ORDER BY s.id`, []string{"id", "stored", "virtual", "changed"}, []string{"INT4", "JSONB", "JSONB", "JSONB"}, [][]any{{int64(1), `["2023-08-15"]`, `["2023-08-15"]`, `["2023-08-15"]`}})
	fixture.JSONPathNavigateBoth(t, p, b, c, check)
	fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT p::text AS path,v,jsonb_path_query_array('"15.08.2023"',p) AS changed FROM jpdt_recipes`, []string{"path", "v", "changed"}, []string{"TEXT", "JSONB", "JSONB"}, [][]any{{`$.datetime("DD.MM.YYYY")`, `["12:34:56.79"]`, `["2023-08-15"]`}}))
	return check
}

func jsonpathDatetimeFuture() []fixture.Step {
	return []fixture.Step{
		fixture.Exec(`INSERT INTO jpdt_stored(id,doc) VALUES(2,'["2023-08-16"]')`, 1),
		jsonpathNavigateSelect(`SELECT changed FROM jpdt_view WHERE id=2`, `["2023-08-16"]`),
		fixture.Exec(`UPDATE jpdt_stored SET doc='["2023-08-17"]' WHERE id=2`, 1),
		fixture.Select(`SELECT stored,virtual FROM jpdt_stored WHERE id=2`, []string{"stored", "virtual"}, []string{"JSONB", "JSONB"}, [][]any{{`["2023-08-17"]`, `["2023-08-17"]`}}),
		fixture.Exec(`INSERT INTO jpdt_recipes(id) VALUES(2)`, 1),
		jsonpathNavigateSelect(`SELECT v AS changed FROM jpdt_recipes WHERE id=2`, `["12:34:56.79"]`),
	}
}

func TestReferenceJSONPathDatetimeStored(t *testing.T) {
	jsonpathDatetimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathDatetimeStored(t, p, b, c)
		d := jsonpathDatetimeFormatError("date", "bad")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(`INSERT INTO jpdt_stored(id,doc) VALUES(2,'["2023-08-16"]'),(3,'["bad"]')`, d))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		for _, s := range jsonpathDatetimeFuture() {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func jsonpathDatetimeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	check := jsonpathDatetimeStored(t, p, b, c)
	future := jsonpathDatetimeFuture()
	dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
	if !b.PG {
		unchanged := func() {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump, after.Bytes()) {
				t.Fatal("failed/canceled temporal Restore published state")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		}
		script := `CREATE TABLE jpdt_bad(v jsonb); INSERT INTO jpdt_bad VALUES(jsonb_path_query_array('"2023-08-15"','$.date()')); INSERT INTO jpdt_bad VALUES(jsonb_path_query_array('"bad"','$.date()'));`
		d := jsonpathDatetimeFormatError("date", "bad")
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
}

func TestReferenceJSONPathDatetimeControls(t *testing.T) {
	jsonpathDatetimeScenario(t, jsonpathDatetimeControls)
}
