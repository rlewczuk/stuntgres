package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func vacuumProgressCapture(t *testing.T, e *core.Engine, observer *session.Session) [][]sqltype.Value {
	t.Helper()
	rows, err := captureVacuumProgress(e, observer)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Error-returning capture is safe inside an admitted Context callback: assertions
// must run only after Execute has unwound and released statement admission.
func captureVacuumProgress(e *core.Engine, observer *session.Session) (rows [][]sqltype.Value, err error) {
	op, err := observer.Acquire(context.Background())
	if err != nil {
		return nil, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, scope.Finish(nil))
	}()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressVacuumOID}
	source, ok := auth.Catalog.Relation(address)
	if !ok {
		e.Mu.Unlock()
		return nil, fmt.Errorf("unregistered real progress descriptor")
	}
	producer, ok := pgcatalog.ProducerFor(address.ObjectID)
	if !ok {
		e.Mu.Unlock()
		return nil, fmt.Errorf("unregistered progress producer")
	}
	rows, err = producer(pgcatalog.Input{Context: context.Background(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: auth.Ref(address), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})})
	e.Mu.Unlock()
	return rows, err
}
func TestVacuumProgressLifecycle(t *testing.T) {
	t.Run("empty_sources", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		for _, view := range []string{"pg_stat_progress_vacuum", "pg_stat_user_tables"} {
			result := sessionSQL(t, s, "SELECT count(*)=0 AS empty FROM pg_catalog."+view)
			defer result.Rows.Close()
			rows := enginetest.Tuples(result)
			if len(rows) != 1 || rows[0][0] != true {
				t.Fatal("actual empty provider source not queryable", view, rows)
			}
		}
	})
	for _, tc := range []struct{ function, phase string }{
		{"storage.InspectVacuum", "scanning heap"},
		{"storage.MaterializeVacuum", "vacuuming heap"},
		{"storage.RemapVacuumIndex", "vacuuming indexes"},
		{"vacuumCompletion", "performing final cleanup"},
	} {
		for _, completion := range []string{"complete", "cancel", "session_close"} {
			name := tc.phase + "/" + completion
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE progress(id INT)", "INSERT INTO progress VALUES(1),(2),(3)", "CREATE INDEX progress_ix ON progress(id)", "DELETE FROM progress WHERE id=1", "CREATE ROLE progress_reader", "CREATE ROLE progress_worker", "ALTER TABLE progress OWNER TO progress_reader", "GRANT MAINTAIN ON progress TO progress_worker"} {
					sessionSQL(t, owner, q)
				}
				sessionSQL(t, worker, "SET ROLE progress_worker")
				task := startVacuumBarrier(t, e, worker, "VACUUM progress", tc.function)
				rows := vacuumProgressCapture(t, e, observer)
				ref, store := vacuumStore(t, e, "progress")
				root, ends, generation, stats := store.Versions, store.Ends, store.Generation, store.Maintenance
				if len(rows) != 1 || len(rows[0]) != 15 {
					t.Fatalf("actual in-flight rows %#v", rows)
				}
				phase, err := rows[0][4].Text()
				if err != nil || phase != tc.phase {
					t.Fatal("not actual entered work phase", phase, err)
				}
				pid, err := rows[0][0].Integer()
				if err != nil || pid != int64(worker.SessionToken.ID()) {
					t.Fatal("synthetic worker PID", pid, err)
				}
				relation, err := rows[0][3].OID()
				if err != nil || relation != uint32(ref.Address.ObjectID) {
					t.Fatal("wrong actual relation", relation, err)
				}
				wantTotal := int64(0)
				if tc.phase == "vacuuming indexes" {
					wantTotal = 1
				}
				vacuumProgressInteger(t, rows[0], 12, wantTotal)
				vacuumProgressInteger(t, rows[0], 13, 0)
				for i := 5; i < 15; i++ {
					if rows[0][i].IsNull() {
						t.Fatalf("authorized progress column %d is NULL", i)
					}
				}
				wantCycles := int64(0)
				if tc.phase == "performing final cleanup" {
					wantCycles = 1
				}
				vacuumProgressInteger(t, rows[0], 8, wantCycles)
				// Neither actual table ownership nor inheriting the maintenance session's
				// current role substitutes for its startup authenticated backend identity.
				for _, role := range []string{"progress_reader", "progress_worker"} {
					sessionSQL(t, observer, "SET ROLE "+role)
					masked := vacuumProgressCapture(t, e, observer)
					if len(masked) != 1 {
						t.Fatal("unauthorized observer lost row identity")
					}
					for i := 3; i < 15; i++ {
						if !masked[0][i].IsNull() {
							t.Fatal("progress detail leaked", role, i, masked[0][i])
						}
					}
				}
				sessionSQL(t, observer, "RESET ROLE")
				sessionSQL(t, observer, "GRANT pg_read_all_stats TO progress_reader")
				sessionSQL(t, observer, "SET ROLE progress_reader")
				readable := vacuumProgressCapture(t, e, observer)
				if len(readable) != 1 || readable[0][3].IsNull() || readable[0][4].IsNull() {
					t.Fatal("inherited statistics authority not honored")
				}
				sessionSQL(t, observer, "RESET ROLE")
				switch completion {
				case "cancel":
					task.cancel()
					task.join(t, context.Canceled)
				case "session_close":
					worker.RequestEnd(session.CleanClose)
					task.join(t, context.Canceled)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := worker.WaitEnd(ctx); err != nil {
						t.Fatal(err)
					}
				default:
					task.join(t, nil)
				}
				if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
					t.Fatal("completed/canceled progress retained", rows)
				}
				if completion != "complete" && (!storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != stats) {
					t.Fatal("canceled/session-ended progress claimed installation or completion")
				}
				retainedPhase, err := rows[0][4].Text()
				if err != nil || retainedPhase != tc.phase {
					t.Fatal("detached typed snapshot mutated after cleanup", retainedPhase, err)
				}
				lockEmpty(t, e)
			})
		}
	}
}
