package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreMetadataComment(t *testing.T) {
	for _, tc := range []struct{ name, comment string }{
		{"table", "COMMENT ON TABLE t IS 'metadata only'"},
		{"column", "COMMENT ON COLUMN t.id IS 'metadata only'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, r := backend.DB, backend.Recorder
					a, b := fixture.LockConnections(t, p.Ctx, db)
					run := func(x fixture.Executor, step fixture.Step) {
						fixture.RunStep(t, p.Ctx, x, r, step)
					}
					run(b, fixture.Exec("CREATE TABLE t(id INT NOT NULL)", 0))
					run(b, fixture.Exec("INSERT INTO t VALUES(1),(2),(2)", 3))
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, fixture.Exec("INSERT INTO t VALUES(7)", 1))
					run(tx, fixture.Exec("DELETE FROM t WHERE id=1", 1))
					// RX and COMMENT's SUE are compatible. Require completion
					// before commit, not a goroutine-start timing assumption.
					run(b, fixture.Exec(tc.comment, 0))
					fixture.DeleteReferenceRows(t, p, b, r, 1, 2, 2)
					fixture.DeleteReferenceRows(t, p, tx, r, 2, 2, 7)
					if err := tx.Commit(); err != nil {
						t.Fatal("metadata-only COMMENT invalidated staged DELETE and prior INSERT", err)
					}
					// Each backend must satisfy this prescribed bag, including
					// the prior private INSERT; parity alone is not sufficient.
					fixture.DeleteReferenceRows(t, p, b, r, 2, 2, 7)
				})
			}
		})
	}
}
