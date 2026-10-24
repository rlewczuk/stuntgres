package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			createSQL := "CREATE TABLE tx_held(v INT DEFAULT 3)"
			create, err := c.PrepareContext(p.Ctx, createSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer create.Close()
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='tx_held' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: createSQL}, b.Recorder, fixture.Exec(createSQL, 0))
			insertSQL := "INSERT INTO tx_held DEFAULT VALUES"
			insert, err := c.PrepareContext(p.Ctx, insertSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			selectSQL := "SELECT * FROM tx_held ORDER BY v"
			reader, err := c.PrepareContext(p.Ctx, selectSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			ins := func() {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insert, SQL: insertSQL}, b.Recorder, fixture.Exec(insertSQL, 1))
			}
			read := func(rows [][]any) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: reader, SQL: selectSQL}, b.Recorder, fixture.Select(selectSQL, []string{"v"}, []string{"INT4"}, rows))
			}
			ins()
			read([][]any{{int64(3)}})
			run(fixture.Exec("ALTER TABLE tx_held ALTER COLUMN v SET DEFAULT 8", 0))
			ins()
			read([][]any{{int64(3)}, {int64(8)}})
			run(fixture.Exec("COMMIT", 0))
			read([][]any{{int64(3)}, {int64(8)}})
			fixture.LockExec(t, p.Ctx, peer, "ALTER TABLE tx_held ADD COLUMN extra INT", 0)
			unexpected, err := reader.QueryContext(p.Ctx)
			if unexpected != nil {
				unexpected.Close()
			}
			fixture.AssertDiagnostic(t, err, func() *fixture.Diagnostic {
				d := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
				return &d
			}())
			fixture.LockExec(t, p.Ctx, peer, "ALTER TABLE tx_held DROP COLUMN extra", 0)
			read([][]any{{int64(3)}, {int64(8)}})
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT before_rename", 0))
			run(fixture.Exec("ALTER TABLE tx_held RENAME TO tx_held_new", 0))
			unexpected, err = reader.QueryContext(p.Ctx)
			if unexpected != nil {
				unexpected.Close()
			}
			fixture.State(t, err, "42P01")
			run(fixture.Exec("ROLLBACK TO before_rename", 0))
			read([][]any{{int64(3)}, {int64(8)}})
			run(fixture.Exec("ROLLBACK", 0))
		})
	}
}
