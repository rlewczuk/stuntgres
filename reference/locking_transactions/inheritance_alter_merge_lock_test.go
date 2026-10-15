package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceAlterMergeStopLocks(t *testing.T) {
	for _, lane := range []string{"nn_existing", "check_default_merge", "check_only_merge"} {
		t.Run(lane, func(t *testing.T) {
			inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
				setup := "ALTER TABLE p ALTER id SET NOT NULL"
				query := "ALTER TABLE p ALTER id SET NOT NULL"
				if lane != "nn_existing" {
					setup = "ALTER TABLE p ADD CONSTRAINT ck CHECK(id>0)"
					query = "ALTER TABLE l ADD CONSTRAINT ck CHECK(id>0)"
				}
				if lane == "check_only_merge" {
					query = "ALTER TABLE ONLY l ADD CONSTRAINT ck CHECK(id>0)"
				}
				fixture.UpdateCoreExec(t, p.Ctx, a, setup, prepared, 0)
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE ONLY child IN ACCESS EXCLUSIVE MODE", 0))
				if lane == "check_default_merge" {
					ap, bp := 0, 0
					if b.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
					}
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, query, prepared) })
					inheritanceQueryWait(t, p, b, ap, bp)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					out := await()
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					fixture.LockAffected(t, out.Result, 0)
				} else {
					// A real conflicting child AX remains held until the successful command
					// completes. The timeout turns accidental child admission into a failure;
					// elapsed time or middleware entry is not used as proof of no conflict.
					fixture.UpdateCoreExec(t, p.Ctx, c, "SET lock_timeout='250ms'", prepared, 0)
					fixture.UpdateCoreExec(t, p.Ctx, c, query, prepared, 0)
					fixture.UpdateCoreExec(t, p.Ctx, c, "SET lock_timeout=0", prepared, 0)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				fixture.QueryLockRows(t, p, b, c, "SELECT id FROM p ORDER BY id", prepared, "id", 1, 2)
			})
		})
	}
}
