package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexPrepared(t *testing.T) {
	transactionalIndexPreparedOwnerWait(t)
	fixture.RoleReferenceCleanup(t, []string{"tx_index_reader"})
	fixture.SchemaReferenceCleanup(t, "tx_index_a")
	fixture.SchemaReferenceCleanup(t, "tx_index_b")
	fixture.SchemaReferenceCleanup(t, "tx_index_c")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalIndexRun(t, p, b, c)
			for _, q := range []string{"CREATE USER tx_index_reader", "CREATE SCHEMA tx_index_a", "CREATE SCHEMA tx_index_b", "CREATE TABLE tx_index_a.t(n INT)", "SET search_path=tx_index_a"} {
				run(fixture.Exec(q, 0))
			}
			q := "CREATE INDEX idx ON t(n)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("ALTER SCHEMA tx_index_b RENAME TO tx_index_c", 0))
			run(fixture.Exec("SET LOCAL search_path=tx_index_c,tx_index_a", 0))
			run(fixture.Exec("CREATE TABLE tx_index_c.t(n INT)", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT schemaname,tablename,indexname FROM pg_indexes WHERE indexname='idx' AND schemaname IN ('tx_index_a','tx_index_c')", []string{"schemaname", "tablename", "indexname"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"tx_index_c", "t", "idx"}}))
			transactionalIndexRun(t, p, b, peer)(fixture.Select("SELECT indexname FROM pg_indexes WHERE indexname='idx' AND schemaname IN ('tx_index_a','tx_index_c')", []string{"indexname"}, []string{"NAME"}, nil))
			run(fixture.Exec("ROLLBACK", 0))
			// A handle prepared with root authority must not retain it at Execute.
			q = "CREATE INDEX denied ON tx_index_a.t(n)"
			denied, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer denied.Close()
			run(fixture.Exec("SET ROLE tx_index_reader", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			// Describe remains syntax-only even while denied and in a read-only block.
			run(fixture.Exec("BEGIN READ ONLY", 0))
			syntax, err := c.PrepareContext(p.Ctx, "CREATE INDEX never ON tx_index_a.missing(n)")
			if err != nil {
				t.Fatal("syntax-only denied/read-only prepare", err)
			}
			syntax.Close()
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN", 0))
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tx_index_a")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: denied, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, d))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: denied, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Exec("ROLLBACK", 0))
			// Conversely, preparation under a denied role does not poison later use.
			run(fixture.Exec("SET ROLE tx_index_reader", 0))
			positive, err := c.PrepareContext(p.Ctx, "CREATE INDEX positive ON tx_index_a.t(n)")
			if err != nil {
				t.Fatal(err)
			}
			defer positive.Close()
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: positive, SQL: "CREATE INDEX positive ON tx_index_a.t(n)"}, b.Recorder, fixture.Exec("CREATE INDEX positive ON tx_index_a.t(n)", 0))
			run(fixture.Exec("COMMIT", 0))
		})
	}
}

func transactionalIndexPreparedOwnerWait(t *testing.T) {
	t.Run("grant-time-owner", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tx_index_owner"})
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				runA := transactionalIndexRun(t, p, b, a)
				runC := transactionalIndexRun(t, p, b, c)
				runA(fixture.Exec("CREATE USER tx_index_owner", 0))
				fixture.WithPublicSchemaOwner(t, p, b, "tx_index_owner", func() {
					runA(fixture.Exec("CREATE TABLE t(n INT)", 0))
					runA(fixture.Exec("ALTER TABLE t OWNER TO tx_index_owner", 0))
					runC(fixture.Exec("SET ROLE tx_index_owner", 0))
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					ap, cp := 0, 0
					var oid int64
					if b.PG {
						ap = fixture.LockPID(t, p.Ctx, a)
						cp = fixture.LockPID(t, p.Ctx, c)
						oid = fixture.LockOID(t, p.Ctx, "t")
					}
					runA(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					runA(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					runA(fixture.Exec("ALTER TABLE t OWNER TO CURRENT_USER", 0))
					const q = "CREATE INDEX idx ON t(n)"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("Describe behind writer", err)
					}
					defer stmt.Close()
					runC(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					done := make(chan struct{})
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						r, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					// ALTER OWNER retains AX; the pending ordinary build still asks SHARE.
					fixture.RelationLockPending(t, p, b, cp, ap, oid, "ShareLock", done)
					runA(fixture.Exec("COMMIT", 0))
					o := await()
					if o.Result != nil {
						t.Fatal("grant-time ownership failure returned Result")
					}
					d := fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")
					fixture.AssertDiagnostic(t, o.Err, &d)
					runC(fixture.Exec("ROLLBACK", 0))
					runA(transactionalIndexName("idx", false))
				})
			})
		}
	})
}
