package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONKeysFeatureSmoke(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jk_smoke(id int,j json)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO jk_smoke VALUES(1,$1),(2,$2),(3,$3)", 3, `{"a":1,"a":2,"b":3}`, `{}`, nil)))
		fixture.JSONKeysBoth(t, p, b, c, jsonKeysSelect("SELECT json_object_keys(j) FROM jk_smoke WHERE id=1", []string{"a", "a", "b"}))
		ordinal := fixture.Select("SELECT k,n FROM jk_smoke,json_object_keys(j) WITH ORDINALITY AS f(k,n) WHERE id=1 ORDER BY n", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(2)}, {"b", int64(3)}})
		ordinal.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonKeysMetadata(t, r, b.PG, ordinal.Columns, ordinal.Types) }
		fixture.JSONKeysBoth(t, p, b, c, ordinal)
		for _, id := range []int{2, 3} {
			s := jsonKeysSelect("SELECT json_object_keys(j) FROM jk_smoke WHERE id=$1", nil, id)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonKeysMetadata(t, r, b.PG, s.Columns, s.Types) }
			fixture.JSONKeysBoth(t, p, b, c, s)
		}
		left := fixture.Select("SELECT id,k FROM jk_smoke LEFT JOIN LATERAL json_object_keys(j) AS f(k) ON true", []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(1), "b"}, {int64(2), nil}, {int64(3), nil}})
		left.Bag = true
		fixture.JSONKeysBoth(t, p, b, c, left)
		jsonKeysHeld(t, p, b, c)
		input := `{"a":{"ignored":"\u0000"}}`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT $1::json AS stored", []string{"stored"}, []string{"JSON"}, [][]any{{input}}, input))
		d := fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
		d.Detail = `\u0000 cannot be converted to text.`
		d.Context = `JSON data, line 1: {"a":{"ignored":"\u0000...`
		fixture.JSONKeysError(t, p, b, c, "SELECT json_object_keys($1)", false, d, "execute", input)
		check := jsonKeysStored(t, p, b, c)
		jsonKeysControls(t, p, b, c, check)
	})
}
