package tablecmds_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func captureFullProgress(t *testing.T, e *core.Engine, observer *session.Session) [][]sqltype.Value {
	t.Helper()
	op, err := observer.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressClusterOID}
	source, ok := auth.Catalog.Relation(address)
	if !ok {
		t.Fatal("cluster progress missing authoritative source")
	}
	produce, ok := pgcatalog.ProducerFor(address.ObjectID)
	if !ok {
		t.Fatal("cluster producer missing")
	}
	rows, err := produce(pgcatalog.Input{Context: op.Context(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: auth.Ref(address), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestVacuumFullAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql, create string
		want        lock.Mode
	}{
		{"VACUUM FULL full_admit", "CREATE TABLE full_admit(id INT)", lock.AccessExclusive},
		{"VACUUM (FULL,PROCESS_MAIN false) full_admit", "CREATE TABLE full_admit(id INT)", lock.AccessExclusive},
		{"VACUUM FULL full_admit", "CREATE TABLE full_admit(id INT) PARTITION BY RANGE(id)", lock.AccessExclusive},
		{"VACUUM (FULL false) full_admit", "CREATE TABLE full_admit(id INT)", lock.ShareUpdateExclusive},
	} {
		t.Run(tc.sql+tc.create, func(t *testing.T) {
			e := engine.New()
			owner, holder, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, tc.create)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "full_admit")
			transactionBegin(t, holder)
			if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, worker, ctx, tc.sql, false)
			lockPending(t, e, 1)
			entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]
			if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != tc.want || entry.Pending[0].Resource.ObjectRef != ref || entry.Pending[0].Provenance.Epoch() != ref.Epoch || entry.Pending[0].Ticket == holder.Transactions.Ticket {
				t.Fatal("wrong live qualified work request", entry)
			}
			if len(captureFullProgress(t, e, observer)) != 0 {
				t.Fatal("progress before work grant")
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			sessionSQL(t, holder, "ROLLBACK")
			lockEmpty(t, e)
		})
	}
}

func TestVacuumFullProgressOwnership(t *testing.T) {
	for _, tc := range []struct {
		boundary, phase           string
		scanned, written, indexes int64
	}{
		{"storage.RewriteVacuum", "seq scanning heap", 0, 0, 0},
		{"index.BuildRelationIndexRoot", "rebuilding index", 3, 2, 0},
		{"vacuumFullCompletion", "rebuilding index", 3, 2, 1},
	} {
		t.Run(tc.boundary, func(t *testing.T) {
			e := engine.New()
			owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE full_progress(id INT)", "INSERT INTO full_progress VALUES(1),(2),(3)", "DELETE FROM full_progress WHERE id=1", "CREATE INDEX full_progress_ix ON full_progress(id)", "CREATE ROLE full_progress_hidden"} {
				sessionSQL(t, owner, q)
			}
			task := startVacuumBarrier(t, e, worker, "VACUUM FULL full_progress", tc.boundary)
			rows := captureFullProgress(t, e, observer)
			if len(rows) != 1 || len(rows[0]) != 12 {
				t.Fatal("actual source row shape", rows)
			}
			phase, err := rows[0][5].Text()
			if err != nil || phase != tc.phase {
				t.Fatal("phase not actually entered", phase, err)
			}
			command, err := rows[0][4].Text()
			if err != nil || command != "VACUUM FULL" {
				t.Fatal(command, err)
			}
			vacuumProgressInteger(t, rows[0], 0, int64(worker.SessionToken.ID()))
			for column, value := range map[int]int64{7: tc.scanned, 8: tc.written, 9: 0, 10: 0, 11: tc.indexes} {
				vacuumProgressInteger(t, rows[0], column, value)
			}
			if len(vacuumProgressCapture(t, e, observer)) != 0 {
				t.Fatal("FULL fabricated ordinary vacuum worker")
			}
			sessionSQL(t, observer, "SET ROLE full_progress_hidden")
			masked := captureFullProgress(t, e, observer)
			if len(masked) != 1 {
				t.Fatal("redaction lost worker identity")
			}
			for i := 3; i < 12; i++ {
				if !masked[0][i].IsNull() {
					t.Fatal("authenticated progress leaked", i)
				}
			}
			sessionSQL(t, observer, "RESET ROLE")
			sessionSQL(t, observer, "GRANT pg_read_all_stats TO full_progress_hidden")
			sessionSQL(t, observer, "SET ROLE full_progress_hidden")
			if visible := captureFullProgress(t, e, observer); len(visible) != 1 || visible[0][3].IsNull() {
				t.Fatal("reader grant ignored")
			}
			sessionSQL(t, observer, "RESET ROLE")
			task.join(t, nil)
			if len(captureFullProgress(t, e, observer)) != 0 {
				t.Fatal("completed progress remains")
			}
			retained, _ := rows[0][5].Text()
			if retained != tc.phase {
				t.Fatal("producer cleanup mutated detached typed backing")
			}
			lockEmpty(t, e)
		})
	}
}
