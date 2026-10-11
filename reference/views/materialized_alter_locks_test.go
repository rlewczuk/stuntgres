package reference_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedAlterLocks(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			alterSeed(run)
			alterExtensionSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("CREATE INDEX alter_lock_index ON alter_mv(i)", 0))
			run(fixture.Exec("SET lock_timeout='100ms'", 0))
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("alter_mv"))
			for _, q := range []string{"ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", "ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", "ALTER MATERIALIZED VIEW alter_mv OWNER TO CURRENT_USER", "ALTER MATERIALIZED VIEW alter_mv SET SCHEMA public", "ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION missing_ext", "ALTER MATERIALIZED VIEW alter_mv NO DEPENDS ON EXTENSION missing_ext"} {
				held, err := peer.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("Describe waited/failed", err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: peer, Statement: held, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
				held.Close()
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("RESET lock_timeout", 0))
			run(materializedViewBag("alter_mv"))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(alterBag("alter_mv", alterThree))
		})
	}
}
func TestReferenceMaterializedAlterCancellation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
			observer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
			alterSeed(run)
			alterExtensionSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("CREATE INDEX alter_cancel_index ON alter_mv(i)", 0))
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("alter_mv"))
			ctx, cancel := context.WithCancel(p.Ctx)
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				tx.Rollback()
				if !joined {
					select {
					case <-done:
					case <-p.Ctx.Done():
						t.Error("ALTER worker failed to join", p.Ctx.Err())
					}
				}
			}()
			pid, blocker := 0, 0
			var oid int64
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, worker)
				blocker = fixture.LockPID(t, p.Ctx, holder)
				oid = fixture.LockOID(t, p.Ctx, "alter_mv")
			}
			go func() {
				_, err := worker.ExecContext(ctx, "ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key")
				done <- err
			}()
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, pid, blocker, oid, "AccessExclusiveLock")
				var signaled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
					t.Fatal(signaled, err)
				}
			} else {
				profiles := make([]runtime.StackRecord, 128)
				observed := false
				for !observed {
					select {
					case err := <-done:
						joined = true
						t.Fatal("ALTER completed before actual wait", err)
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					default:
					}
					n, ok := runtime.GoroutineProfile(profiles)
					if !ok {
						profiles = make([]runtime.StackRecord, n)
						continue
					}
					for _, profile := range profiles[:n] {
						frames := runtime.CallersFrames(profile.Stack())
						wait, worker := false, false
						for {
							frame, more := frames.Next()
							wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
							worker = worker || strings.Contains(frame.Function, "TestReferenceMaterializedAlterCancellation")
							if !more {
								break
							}
						}
						if wait && worker {
							observed = true
							break
						}
					}
					if !observed {
						runtime.Gosched()
					}
				}
				cancel()
			}
			select {
			case err := <-done:
				joined = true
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, err, &want)
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-p.Ctx.Done():
				t.Fatal("ALTER cancellation join", p.Ctx.Err())
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(materializedViewBag("alter_mv"))
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("canceled ALTER changed Dump")
				}
			}
			alterX(run, "alter_mv", 1)
			fixture.MaterializedViewPopulation(run, "alter_mv", true)
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_mv", 0))
			run(alterBag("alter_mv", alterThree))
		})
	}
}
