package reference_test

import (
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func transactionalSchemaFailedRestoreCommit(t *testing.T) {
	t.Run("failed_restore_commit", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE SEQUENCE tx_restore_counter", 0))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("CREATE TABLE tx_restore_eligible(id SERIAL PRIMARY KEY,v INT DEFAULT 3)", 0))
				run(fixture.Exec("INSERT INTO tx_restore_eligible(v) VALUES(DEFAULT)", 1))
				if !b.PG {
					readErr := errors.New("private DDL reader failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, addConstraintFailedReader{readErr}); !errors.Is(err, readErr) {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE tx_late_restore(v INT); INSERT INTO tx_late_restore VALUES('bad');")); err == nil {
						t.Fatal("late restore error missing")
					}
				}
				run(fixture.Exec("ALTER TABLE tx_restore_eligible ALTER COLUMN v SET DEFAULT 8", 0))
				run(fixture.Exec("INSERT INTO tx_restore_eligible(v) VALUES(DEFAULT)", 1))
				run(fixture.Exec("COMMIT", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,v FROM tx_restore_eligible ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(8)}}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE TABLE tx_restore_rolled_back(id SERIAL)", 0))
				run(fixture.Exec("INSERT INTO tx_restore_rolled_back DEFAULT VALUES", 1))
				run(fixture.Exec("SAVEPOINT counter_scope", 0))
				run(fixture.Select("SELECT setval('tx_restore_counter',40,true)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(40)}}))
				run(fixture.Exec("ROLLBACK TO counter_scope", 0))
				run(fixture.Select("SELECT nextval('tx_restore_counter')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(41)}}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT currval('tx_restore_counter')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(41)}}))
				run(fixture.Select("SELECT nextval('tx_restore_counter')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(42)}}))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('tx_restore_rolled_back','tx_restore_rolled_back_id_seq','tx_late_restore')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		}
	})
}
