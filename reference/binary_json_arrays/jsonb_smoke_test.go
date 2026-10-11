package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jb_smoke(id INT UNIQUE,v JSONB)", 0))
		run(fixture.Exec(`INSERT INTO jb_smoke VALUES(1,' {"z":1,"a":1,"a":2} ')`, 1))
		q := "INSERT INTO jb_smoke VALUES($1,$2)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
		buf := []byte(`[1,true,null]`)
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 2, buf))
		buf[0] = 'x'
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 3, "null"))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 4, nil))
		run(fixture.Select("SELECT id,v FROM jb_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 2, "z": 1}`}, {int64(2), `[1, true, null]`}, {int64(3), "null"}, {int64(4), nil}}))
		run(fixture.Prepared(fixture.Select("SELECT id FROM jb_smoke WHERE v=$1::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, `{"z":1,"a":2}`)))
		run(fixture.Select("SELECT id FROM jb_smoke WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
		run(fixture.Prepared(fixture.Select("SELECT id FROM jb_smoke WHERE v='null'::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})))
		run(fixture.Select(`SELECT '1'::jsonb='1.00'::jsonb AS equal,'[9]'::jsonb<'[0,0]'::jsonb AS shorter`, []string{"equal", "shorter"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		source := ` {"z":1,"a":1,"a":2} `
		run(fixture.Prepared(fixture.Select("SELECT $1::json AS original,$1::json::jsonb AS normalized", []string{"original", "normalized"}, []string{"JSON", "JSONB"}, [][]any{{source, `{"a": 2, "z": 1}`}}, source)))
		jsonbReferenceGrouping(t, p, backend)
		jsonbReferenceAtomicity(t, p, backend)
		jsonbReferenceControlSeed(t, p, backend)
	}
	jsonbReferenceNativeControls(t, p, true)
}
