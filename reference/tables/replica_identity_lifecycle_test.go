package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicaIdentityTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			see := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
			replicaSetup(run)
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
			run(fixture.Exec("BEGIN", 0))
			defer a.ExecContext(context.Background(), "ROLLBACK")
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY FULL", 0))
			run(replicaMode("ri_t", "f"))
			run(replicaFlags(""))
			see(replicaMode("ri_t", "i"))
			see(replicaFlags("ri_k"))
			run(fixture.Exec("ROLLBACK", 0))
			run(replicaMode("ri_t", "i"))
			run(replicaFlags("ri_k"))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_bk", 0))
			run(fixture.Exec("SAVEPOINT ri_save", 0))
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY NOTHING", 0))
			run(replicaMode("ri_t", "n"))
			run(fixture.Exec("ROLLBACK TO ri_save", 0))
			run(replicaMode("ri_t", "i"))
			run(replicaFlags("ri_bk"))
			run(replicaMissing("ALTER TABLE ri_t REPLICA IDENTITY FULL, REPLICA IDENTITY USING INDEX ri_missing"))
			run(fixture.Exec("ROLLBACK TO ri_save", 0))
			run(replicaFlags("ri_bk"))
			run(fixture.Exec("COMMIT", 0))
			see(replicaMode("ri_t", "i"))
			see(replicaFlags("ri_bk"))
			see(replicaData())
		})
	}
}

func TestReferenceReplicaIdentityPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			replicaSetup(run)
			q := "ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(replicaFlags("ri_k"))
			run(fixture.Exec("ALTER INDEX ri_k RENAME TO ri_renamed", 0))
			want := fixture.Diagnostic{Code: "42704", Message: `index "ri_k" for table "ri_t" does not exist`}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, want))
			run(fixture.Exec("CREATE UNIQUE INDEX ri_k ON ri_t(k)", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Select("SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_t'::regclass AND i.indisreplident", []string{"relname"}, []string{"NAME"}, [][]any{{"ri_k"}}))
			run(fixture.Exec("DROP INDEX ri_k", 0))
			run(replicaMode("ri_t", "i"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, want))
			run(fixture.Exec("CREATE INDEX ri_k ON ri_t(k)", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42809", Message: `cannot use non-unique index "ri_k" as replica identity`}))
			run(replicaData())
		})
	}
}

func TestReferenceReplicaIdentityLifecycle(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(fmt.Sprint(attached), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "ri_moved")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE ri_t(k INT CONSTRAINT ri_nn NOT NULL,p TEXT)", 0))
					run(fixture.Exec("INSERT INTO ri_t VALUES(1,'one')", 1))
					index := "ri_k"
					if attached {
						index = "ri_u"
						run(fixture.Exec("ALTER TABLE ri_t ADD CONSTRAINT ri_u UNIQUE(k)", 0))
					} else {
						run(fixture.Exec("CREATE UNIQUE INDEX ri_k ON ri_t(k) INCLUDE(p)", 0))
					}
					run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX "+index, 0))
					for _, q := range []string{"ALTER TABLE ri_t ALTER k DROP NOT NULL", "ALTER TABLE ri_t DROP CONSTRAINT ri_nn", "ALTER TABLE ri_t REPLICA IDENTITY FULL, ALTER k DROP NOT NULL"} {
						run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P16", Message: `column "k" is in index used as replica identity`}))
					}
					run(fixture.Exec("ALTER TABLE ri_t ALTER k TYPE BIGINT", 0))
					run(fixture.Select("SELECT k,p FROM ri_t", []string{"k", "p"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "one"}}))
					run(fixture.Select("SELECT c.relname,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_t'::regclass", []string{"relname", "indisreplident"}, []string{"NAME", "BOOL"}, [][]any{{index, true}}))
					run(fixture.Exec("REINDEX INDEX "+index, 0))
					run(fixture.Exec("ALTER INDEX "+index+" RENAME TO ri_final", 0))
					run(fixture.Exec("ALTER TABLE ri_t RENAME COLUMN k TO key", 0))
					run(fixture.Exec("ALTER TABLE ri_t RENAME TO ri_final_t", 0))
					run(fixture.Exec("CREATE SCHEMA ri_moved", 0))
					run(fixture.Exec("ALTER TABLE ri_final_t SET SCHEMA ri_moved", 0))
					run(replicaMode("ri_moved.ri_final_t", "i"))
					run(fixture.Select("SELECT c.relname,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_moved.ri_final_t'::regclass", []string{"relname", "indisreplident"}, []string{"NAME", "BOOL"}, [][]any{{"ri_final", true}}))
					if attached {
						run(fixture.Exec("ALTER TABLE ri_moved.ri_final_t DROP CONSTRAINT ri_final", 0))
					} else {
						run(fixture.Exec("DROP INDEX ri_moved.ri_final", 0))
					}
					// Live orphan-i is exact even though ordinary SQL cannot replay it.
					run(replicaMode("ri_moved.ri_final_t", "i"))
					run(fixture.Select("SELECT indisreplident FROM pg_index WHERE indrelid='ri_moved.ri_final_t'::regclass", []string{"indisreplident"}, []string{"BOOL"}, nil))
					run(fixture.Exec("ALTER TABLE ri_moved.ri_final_t REPLICA IDENTITY FULL", 0))
					run(fixture.Exec("ALTER TABLE ri_moved.ri_final_t ALTER key DROP NOT NULL", 0))
				})
			}
		})
	}
	t.Run("hierarchy", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, q := range []string{"CREATE TABLE ri_parent(k INT)", "CREATE TABLE ri_child() INHERITS(ri_parent)", "ALTER TABLE ri_child REPLICA IDENTITY NOTHING", "ALTER TABLE ri_parent REPLICA IDENTITY FULL", "CREATE TABLE ri_part(k INT) PARTITION BY RANGE(k)", "CREATE TABLE ri_leaf PARTITION OF ri_part FOR VALUES FROM(0) TO(10)", "ALTER TABLE ri_part REPLICA IDENTITY FULL"} {
			p.Step(t, fixture.Exec(q, 0))
		}
		p.Step(t, replicaMode("ri_parent", "f"))
		p.Step(t, replicaMode("ri_child", "n"))
		p.Step(t, replicaMode("ri_part", "f"))
		p.Step(t, replicaMode("ri_leaf", "d"))
		p.Step(t, fixture.Exec("ALTER TABLE ONLY ri_parent REPLICA IDENTITY DEFAULT", 0))
		p.Step(t, fixture.Exec("ALTER TABLE ri_parent * REPLICA IDENTITY FULL", 0))
		p.Step(t, replicaMode("ri_child", "n"))
	})
}

func TestReferenceReplicaIdentityCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					blocker, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, blocker, b.Recorder, s) }
					replicaSetup(run)
					run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
					q := "ALTER TABLE ri_t REPLICA IDENTITY FULL"
					statement, err := waiter.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					run(fixture.Exec("BEGIN", 0))
					defer blocker.ExecContext(context.Background(), "ROLLBACK")
					run(replicaData())
					fixture.RelationLockActiveCancel(t, p, b, waiter, q, func(ctx context.Context) fixture.RelationOutcome {
						var outcome fixture.RelationOutcome
						if prepared {
							outcome.Result, outcome.Err = statement.ExecContext(ctx)
						} else {
							outcome.Result, outcome.Err = waiter.ExecContext(ctx, q)
						}
						return outcome
					})
					run(fixture.Exec("ROLLBACK", 0))
					run(replicaMode("ri_t", "i"))
					run(replicaFlags("ri_k"))
					run(replicaData())
					fixture.LockExec(t, p.Ctx, waiter, "SET lock_timeout='10ms'", 0)
					run(fixture.Exec("BEGIN", 0))
					run(replicaData())
					fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
					run(fixture.Exec("ROLLBACK", 0))
					run(replicaMode("ri_t", "i"))
					run(replicaFlags("ri_k"))
					run(replicaData())
				})
			}
		})
	}
}
