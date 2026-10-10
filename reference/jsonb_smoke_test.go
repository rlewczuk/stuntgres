package reference_test

import "testing"

func TestReferenceJSONBFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE jb_smoke(id INT UNIQUE,v JSONB)", 0))
		run(referenceExec(`INSERT INTO jb_smoke VALUES(1,' {"z":1,"a":1,"a":2} ')`, 1))
		q := "INSERT INTO jb_smoke VALUES($1,$2)"
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
		buf := []byte(`[1,true,null]`)
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 2, buf))
		buf[0] = 'x'
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 3, "null"))
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 4, nil))
		run(referenceSelect("SELECT id,v FROM jb_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 2, "z": 1}`}, {int64(2), `[1, true, null]`}, {int64(3), "null"}, {int64(4), nil}}))
		run(referencePrepared(referenceSelect("SELECT id FROM jb_smoke WHERE v=$1::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, `{"z":1,"a":2}`)))
		run(referenceSelect("SELECT id FROM jb_smoke WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
		run(referencePrepared(referenceSelect("SELECT id FROM jb_smoke WHERE v='null'::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})))
		run(referenceSelect(`SELECT '1'::jsonb='1.00'::jsonb AS equal,'[9]'::jsonb<'[0,0]'::jsonb AS shorter`, []string{"equal", "shorter"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		source := ` {"z":1,"a":1,"a":2} `
		run(referencePrepared(referenceSelect("SELECT $1::json AS original,$1::json::jsonb AS normalized", []string{"original", "normalized"}, []string{"JSON", "JSONB"}, [][]any{{source, `{"a": 2, "z": 1}`}}, source)))
		jsonbReferenceGrouping(t, p, backend)
		jsonbReferenceAtomicity(t, p, backend)
		jsonbReferenceControlSeed(t, p, backend)
	}
	jsonbReferenceNativeControls(t, p, true)
}
