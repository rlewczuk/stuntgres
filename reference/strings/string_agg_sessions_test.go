package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringAggSessionConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			stringAggSetup(run)
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			const q = "SELECT string_agg(v,$1 ORDER BY id),string_agg(b,$2 ORDER BY id) FROM sa"
			s := fixture.Select(q, []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{"a//b/c", string([]byte{0, 92, 92, 255, 92, 128})}}, "/", []byte{92})
			s.CheckMetadata = stringAggMetadata(backend.PG)
			for _, prepared := range []bool{false, true} {
				step := s
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, delimiter := range []string{"/", "!"} {
				step := s
				step.Args = []any{delimiter, []byte{92}}
				step.Rows = [][]any{{"a" + delimiter + delimiter + "b" + delimiter + "c", string([]byte{0, 92, 92, 255, 92, 128})}}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, backend.Recorder, step)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			for _, plain := range []bool{false, true} {
				t.Run(map[bool]string{false: "tx-context", true: "tx-plain"}[plain], func(t *testing.T) {
					tx, err := backend.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					for _, prepared := range []bool{false, true} {
						step := s
						step.Prepared = prepared
						step.NonContext = plain
						fixture.RunStep(t, p.Ctx, tx, backend.Recorder, step)
					}
					insert := fixture.Exec("INSERT INTO sa VALUES(2,7,'z','?',NULL,NULL)", 1)
					insert.NonContext = plain
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, insert)
					group := fixture.Select("SELECT k,string_agg(v,d ORDER BY id) FROM sa GROUP BY k ORDER BY k", []string{"k", "string_agg"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a!b/c"}, {int64(2), "z"}})
					group.NonContext = plain
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, group)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(fixture.Select("SELECT k,string_agg(v,d ORDER BY id) FROM sa GROUP BY k ORDER BY k", []string{"k", "string_agg"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a!b/c"}}))
		})
	}
}
