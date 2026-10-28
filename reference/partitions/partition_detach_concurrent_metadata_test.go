package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func detachConcurrentMetadata(t *testing.T) {
	for _, plain := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run("metadata/"+map[bool]string{false: "context", true: "plain"}[plain]+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					detachConcurrentSetup(run)
					q := "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY"
					var e fixture.Executor = b.DB
					if held {
						var stmt *sql.Stmt
						var err error
						if plain {
							stmt, err = b.DB.Prepare(q)
						} else {
							stmt, err = b.DB.PrepareContext(p.Ctx, q)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						// Preparing a utility must not execute phase one or publish any CHECK.
						detachConcurrentState(run, true, false)
						run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND contype='c' AND conislocal", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						e = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					}
					s := fixture.Select(q, []string{}, []string{}, [][]any{})
					s.NonContext = plain
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					detachConcurrentState(run, false, false)
				})
			})
		}
	}
}
