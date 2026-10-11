package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshSnapshots(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			if b.PG {
				// A real same-session SQL portal is active, unlike native eager Rows.
				tx := fixture.QueryLockBegin(t, p.Ctx, conn)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE refresh_cursor CURSOR FOR SELECT i,s FROM refresh_mv ORDER BY i", 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH 1 FROM refresh_cursor", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_mv", fixture.ControlsReferenceDiagnostic("55006", `cannot REFRESH MATERIALIZED VIEW "refresh_mv" because it is being used by active queries in this session`)))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(materializedViewBag("refresh_mv"))
				return
			}
			retained, err := p.Native.QueryContext(p.Ctx, "SELECT i,s FROM refresh_mv ORDER BY i")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			cols, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, false, cols, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true})
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
			run(refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA", 0))
			run(fixture.MaterializedViewUnpopulated("refresh_mv"))
			materializedViewRetainedBag(t, retained)
			fixture.TableDDLColumnTypes(t, false, cols, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true})
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
		})
	}
}

func TestReferenceMaterializedRefreshControls(t *testing.T) { refreshControls(t) }

func refreshControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
			four := refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}})
			run(fixture.Exec("INSERT INTO mv_base VALUES(4,'diverged')", 1))
			run(four)
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_control_no AS SELECT i FROM mv_base WITH NO DATA", 0))
			check := func() {
				run(four)
				run(fixture.Prepared(four))
				fixture.MaterializedViewPopulation(run, "refresh_mv", true)
				run(fixture.Select("SELECT count(*) AS n FROM mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
				run(fixture.MaterializedViewUnpopulated("refresh_control_no"))
				fixture.MaterializedViewPopulation(run, "refresh_control_no", false)
				run(fixture.Select("SELECT pg_get_viewdef('refresh_mv'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT i,\n    s\n   FROM mv_base;"}}))
			}
			check()
			if !b.PG {
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				for range 2 {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					check()
				}
				fresh := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, four)
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.MaterializedViewUnpopulated("refresh_control_no"))
				run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA", 0))
				var empty bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &empty); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(empty.Bytes())); err != nil {
					t.Fatal(err)
				}
				run(fixture.MaterializedViewUnpopulated("refresh_mv"))
				fixture.MaterializedViewPopulation(run, "refresh_mv", false)
				// Restore's SQL replay whitelist is unchanged: REFRESH is not import authority.
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("REFRESH MATERIALIZED VIEW refresh_mv;")); err == nil {
					t.Fatal("Restore accepted REFRESH utility")
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("refused Restore changed catalog")
				}
			} else if dump != nil {
				// Replay actual native emitted SQL, not a surrogate dump or PG Restore API.
				run(fixture.Exec("DROP MATERIALIZED VIEW refresh_mv,refresh_control_no", 0))
				run(fixture.Exec("DROP TABLE mv_base", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected native dump", err)
				}
				run(fixture.MaterializedViewUnpopulated("refresh_mv"))
				run(fixture.MaterializedViewUnpopulated("refresh_control_no"))
				fixture.MaterializedViewPopulation(run, "refresh_mv", false)
				run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
				run(refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}, {int64(4), "diverged"}}))
			}
		})
	}
}

func TestReferenceMaterializedRefreshFeatureSmoke(t *testing.T) {
	// Compose real feature scenarios; this is contract coverage, not the standalone Go proof.
	t.Run("lifecycle", refreshLifecycle)
	t.Run("population", TestReferenceMaterializedRefreshPopulation)
	t.Run("atomicity", TestReferenceMaterializedRefreshAtomicity)
	t.Run("owner", TestReferenceMaterializedRefreshOwnerContext)
	t.Run("authorization", TestReferenceMaterializedRefreshAuthorization)
	t.Run("metadata", TestReferenceMaterializedRefreshMetadata)
	t.Run("snapshots", TestReferenceMaterializedRefreshSnapshots)
	t.Run("locks", TestReferenceMaterializedRefreshLocks)
	t.Run("queued", TestReferenceMaterializedRefreshQueuedReplacement)
	t.Run("cancellation", TestReferenceMaterializedRefreshCancellation)
	t.Run("boundaries", TestReferenceMaterializedRefreshBoundaries)
	t.Run("transactions", TestReferenceMaterializedRefreshTransactions)
	t.Run("controls", refreshControls)
}
