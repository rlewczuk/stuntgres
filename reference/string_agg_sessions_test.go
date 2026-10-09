package reference_test

import "testing"

func TestReferenceStringAggSessionConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			stringAggSetup(run)
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			const q = "SELECT string_agg(v,$1 ORDER BY id),string_agg(b,$2 ORDER BY id) FROM sa"
			s := referenceSelect(q, []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{"a//b/c", string([]byte{0, 92, 92, 255, 92, 128})}}, "/", []byte{92})
			s.checkMetadata = stringAggMetadata(backend.pg)
			for _, prepared := range []bool{false, true} {
				step := s
				step.prepared = prepared
				referenceRunStep(t, p.ctx, conn, backend.recorder, step)
			}
			held, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, delimiter := range []string{"/", "!"} {
				step := s
				step.args = []any{delimiter, []byte{92}}
				step.rows = [][]any{{"a" + delimiter + delimiter + "b" + delimiter + "c", string([]byte{0, 92, 92, 255, 92, 128})}}
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: q}, backend.recorder, step)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			for _, plain := range []bool{false, true} {
				t.Run(map[bool]string{false: "tx-context", true: "tx-plain"}[plain], func(t *testing.T) {
					tx, err := backend.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					for _, prepared := range []bool{false, true} {
						step := s
						step.prepared = prepared
						step.nonContext = plain
						referenceRunStep(t, p.ctx, tx, backend.recorder, step)
					}
					insert := referenceExec("INSERT INTO sa VALUES(2,7,'z','?',NULL,NULL)", 1)
					insert.nonContext = plain
					referenceRunStep(t, p.ctx, tx, backend.recorder, insert)
					group := referenceSelect("SELECT k,string_agg(v,d ORDER BY id) FROM sa GROUP BY k ORDER BY k", []string{"k", "string_agg"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a!b/c"}, {int64(2), "z"}})
					group.nonContext = plain
					referenceRunStep(t, p.ctx, tx, backend.recorder, group)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(referenceSelect("SELECT k,string_agg(v,d ORDER BY id) FROM sa GROUP BY k ORDER BY k", []string{"k", "string_agg"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a!b/c"}}))
		})
	}
}
