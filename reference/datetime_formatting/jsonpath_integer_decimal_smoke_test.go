package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathIntegerDecimalFeatureSmoke(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, want string }{
			{`[1.5,-1.5]`, `$[*].integer()`, `[2, -2]`},
			{`[9223372036854775807,-9223372036854775808]`, `$[*].bigint()`, `[9223372036854775807, -9223372036854775808]`},
			{`[1.25,-1.25]`, `$[*].decimal(3,1)`, `[1.3, -1.3]`},
			{`1500`, `$.decimal(2,-3)`, `[2000]`},
			{`0.000004`, `$.decimal(3,5)`, `[0.00000]`},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(`SELECT jsonb_path_query_array($1,$2) AS changed`, tc.want, tc.doc, tc.path), b.PG))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array('99.95','$.decimal(3,1)')`, "22036", integerDecimalInvalid("decimal", "99.95")))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('1','$.decimal(0)',silent=>true) AS changed`, `[]`))
		q := `SELECT jsonb_path_query_array($1,$2,'{}',$3) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			jsonpathNavigateError(q, "22036", integerDecimalInvalid("integer", "bad"), `[1,"bad",2]`, `$[*].integer()`, false),
			jsonpathNavigateSelect(q, `[1]`, `[1,"bad",2]`, `$[*].integer()`, true),
			jsonpathNavigateSelect(q, `[2, -2]`, `[1.5,-1.5]`, `$[*].integer()`, false),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE jpid_smoke(doc jsonb,path jsonpath DEFAULT '$[*].decimal(6,2)')`, 0),
			fixture.Exec(`INSERT INTO jpid_smoke(doc) VALUES('[1.235,-2.345]')`, 1),
			fixture.Exec(`CREATE VIEW jpid_smoke_view AS SELECT jsonb_path_query_array(doc,path) AS changed FROM jpid_smoke`, 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		check := fixture.JSONPathNavigateViewMetadataStep(jsonpathNavigateSelect(`SELECT changed FROM jpid_smoke_view`, `[1.24, -2.35]`), b.PG)
		fixture.JSONPathNavigateBoth(t, p, b, c, check)
		future := []fixture.Step{fixture.Exec(`UPDATE jpid_smoke SET doc='[3.456]'`, 1), jsonpathNavigateSelect(`SELECT changed FROM jpid_smoke_view`, `[3.46]`)}
		fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
