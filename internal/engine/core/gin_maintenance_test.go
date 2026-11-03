package core_test

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
	"testing"
	"time"
)

func ginCoreRelation(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	for _, descriptor := range e.CatalogRead().Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if ok && relation.Name() == name {
			namespace, _ := e.CatalogRead().Namespace(relation.Namespace())
			if namespace.Name() == "public" {
				return relation
			}
		}
	}
	t.Fatal("missing GIN test relation", name)
	return nil
}
func ginCoreFixture(t *testing.T) (*core.Engine, *session.Session, *session.Session, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE gin_cohort(id integer,a integer[])")
	enginetest.SessionSQL(t, a, "CREATE INDEX gin_cohort_i ON gin_cohort USING gin(a)")
	return e, a, b, ginCoreRelation(t, e, "gin_cohort_i")
}
func ginCorePending(t *testing.T, e *core.Engine, target *catalog.Relation, want int64, members int) *storage.IndexRoot {
	t.Helper()
	e.Mu.Lock()
	root := e.Storage.IndexRoots[target.Address()]
	e.Mu.Unlock()
	if root == nil || root.Gin == nil || storage.GinPendingIncidences(root.Gin) != want || len(root.Entries) != members {
		t.Fatal("split/membership", root, want, members)
	}
	return root
}
func ginCoreWrites(owner *session.TransactionOwner, address ids.ObjectAddress) []storage.RowVersion {
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	return owner.Writes[address]
}
func ginCoreOutput(t *testing.T, rows []storage.RowVersion, target *catalog.Relation, pending bool) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("missing genuine private rows")
	}
	for _, row := range rows {
		output, ok := row.IndexOutputs[target.Address()]
		if !ok || !output.Member || output.GinPending != pending || !index.OutputMatches(output, target) {
			t.Fatal("private produced placement", row.Row, row.Version, output)
		}
	}
}
func ginCoreClean(t *testing.T, actor *session.Session) {
	t.Helper()
	enginetest.SessionSQL(t, actor, "SELECT gin_clean_pending_list('gin_cohort_i'::regclass)")
}
func ginCoreAwaitPause(t *testing.T, pause *util.ExecutionPause) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pause.Wait(ctx); err != nil {
		t.Fatal("work did not reach exact preparation", err)
	}
}

func TestGINMaintenancePublication(t *testing.T) {
	t.Run("private-only-cohort-commit", func(t *testing.T) {
		e, a, b, target := ginCoreFixture(t)
		shared := ginCorePending(t, e, target, 0, 0)
		enginetest.SessionSQL(t, a, "BEGIN")
		enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{1,2,2}')")
		enginetest.SessionSQL(t, a, "SAVEPOINT before_clean")
		old := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
		ginCoreOutput(t, old, target, true)
		beforeFrontier := e.Visibility.CommitSeq()
		beforeCatalog := e.CatalogRead().State()
		ginCoreClean(t, b)
		current := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
		ginCoreOutput(t, current, target, false)
		ginCoreOutput(t, old, target, true)
		if storage.SameVersionRoot(current, old) || &current[0].Values[0] != &old[0].Values[0] || &current[0].IndexOutputs[target.Address()].Keys[0] != &old[0].IndexOutputs[target.Address()].Keys[0] {
			t.Fatal("cleanup copied values/keys or mutated old output")
		}
		a.Transactions.Mu.Lock()
		saved := a.Transactions.Savepoints[0].Writes[target.SQLIndex.Parent]
		revision := a.Transactions.GinRevision
		a.Transactions.Mu.Unlock()
		ginCoreOutput(t, saved, target, false)
		if revision == 0 || e.Storage.IndexRoots[target.Address()] != shared || e.Visibility.CommitSeq() != beforeFrontier || e.CatalogRead().State() != beforeCatalog {
			t.Fatal("private cleanup published membership/catalog/frontier or missed fence")
		}
		enginetest.SessionSQL(t, a, "ROLLBACK TO before_clean")
		enginetest.SessionSQL(t, a, "COMMIT")
		ginCorePending(t, e, target, 0, 1)
		enginetest.SessionSQL(t, b, "INSERT INTO gin_cohort VALUES(2,'{3}')")
		ginCorePending(t, e, target, 1, 2)
		ginCoreClean(t, b)
		ginCorePending(t, e, target, 0, 2)
	})
	t.Run("private-rollback-and-history", func(t *testing.T) {
		e, a, b, target := ginCoreFixture(t)
		enginetest.SessionSQL(t, a, "BEGIN")
		enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{1,2}')")
		enginetest.SessionSQL(t, a, "SAVEPOINT previous")
		enginetest.SessionSQL(t, a, "UPDATE gin_cohort SET a='{3}' WHERE id=1")
		old := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
		if old[0].PrivateHistory == nil {
			t.Fatal("missing genuine update predecessor")
		}
		ginCoreClean(t, b)
		rows := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
		ginCoreOutput(t, rows, target, false)
		if rows[0].PrivateHistory.Version.IndexOutputs[target.Address()].GinPending || !old[0].PrivateHistory.Version.IndexOutputs[target.Address()].GinPending {
			t.Fatal("history was not COW cleaned")
		}
		enginetest.SessionSQL(t, a, "ROLLBACK TO previous")
		ginCoreOutput(t, ginCoreWrites(a.Transactions, target.SQLIndex.Parent), target, false)
		enginetest.SessionSQL(t, a, "DELETE FROM gin_cohort WHERE id=1")
		ginCoreClean(t, b)
		enginetest.SessionSQL(t, a, "ROLLBACK")
		ginCorePending(t, e, target, 0, 0)
	})
	t.Run("read-only-and-failed-select", func(t *testing.T) {
		e, a, _, target := ginCoreFixture(t)
		enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{1,2}')")
		pending := ginCorePending(t, e, target, 2, 1)
		enginetest.SessionSQL(t, a, "BEGIN READ ONLY")
		ginCoreClean(t, a)
		enginetest.SessionSQL(t, a, "ROLLBACK")
		clean := ginCorePending(t, e, target, 0, 1)
		if pending == clean || storage.GinPendingIncidences(pending.Gin) != 2 {
			t.Fatal("cleanup did not transfer immutable root")
		}
		enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(2,'{2}')")
		op, err := a.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, enginetest.Statement(t, "WITH cleaned AS MATERIALIZED (SELECT gin_clean_pending_list('gin_cohort_i'::regclass) AS n) SELECT CASE WHEN n>0 THEN n/(n-n) ELSE NULL END FROM cleaned"), 0, nil)
		op.Done()
		if err == nil {
			t.Fatal("downstream runtime failure not reached")
		}
		ginCorePending(t, e, target, 0, 2)
	})
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured-commit", true: "claimed-prepared-finisher"}[prepared], func(t *testing.T) {
			e, a, b, target := ginCoreFixture(t)
			enginetest.SessionSQL(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2}')")
			command := "COMMIT"
			worker := a
			var detached *session.PreparedTransaction
			if prepared {
				enginetest.SessionSQL(t, a, "PREPARE TRANSACTION 'gin_detached'")
				e.Mu.Lock()
				detached = core.EnginePreparedRef(e).Lookup("gin_detached")
				e.Mu.Unlock()
				worker = enginetest.StartSession(t, e, nil)
				command = "COMMIT PREPARED 'gin_detached'"
			}
			old := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
			if prepared {
				old = detached.Writes[target.SQLIndex.Parent]
			}
			pause := util.NewExecutionPause("gin.publication.prepared")
			defer pause.Resume()
			await := enginetest.ExecuteAsync(t, e, worker, util.WithExecutionPause(context.Background(), pause), command)
			ginCoreAwaitPause(t, pause)
			ginCoreClean(t, b)
			ginCorePending(t, e, target, 0, 0)
			if prepared {
				e.Mu.Lock()
				latest := detached.Writes[target.SQLIndex.Parent]
				revision, claimed := detached.GinRevision, detached.Claimed
				e.Mu.Unlock()
				ginCoreOutput(t, latest, target, false)
				if revision == 0 || !claimed {
					t.Fatal("claimed owner was skipped")
				}
			} else {
				ginCoreOutput(t, ginCoreWrites(a.Transactions, target.SQLIndex.Parent), target, false)
			}
			ginCoreOutput(t, old, target, true)
			pause.Resume()
			result, err := await()
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			ginCorePending(t, e, target, 0, 1)
		})
	}
	t.Run("projected-stage-alias", func(t *testing.T) {
		e, a, b, target := ginCoreFixture(t)
		enginetest.SessionSQL(t, a, "BEGIN")
		enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2}')")
		pause := util.NewExecutionPause("gin.statement.validated")
		defer pause.Resume()
		await := enginetest.ExecuteAsync(t, e, a, util.WithExecutionPause(context.Background(), pause), "ALTER TABLE gin_cohort ADD COLUMN extra integer DEFAULT 7")
		ginCoreAwaitPause(t, pause)
		ginCoreClean(t, b)
		pause.Resume()
		result, err := await()
		if result.Rows != nil {
			result.Rows.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		ginCoreOutput(t, ginCoreWrites(a.Transactions, target.SQLIndex.Parent), target, false)
		enginetest.SessionSQL(t, a, "COMMIT")
		ginCorePending(t, e, target, 0, 1)
	})
	t.Run("all-owner-cancellation", ginMaintenanceCohortCancellation)
	t.Run("live-receipt-transition", ginMaintenanceLiveReceipt)
	t.Run("private-presentation-cohort", ginMaintenancePresentationCohort)
	t.Run("returning-stage-producer", ginMaintenanceReturningStage)
	t.Run("foreign-committed-presentation", ginMaintenanceForeignPresentation)
}
func ginMaintenanceCohortCancellation(t *testing.T) {
	e, a, b, target := ginCoreFixture(t)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "INSERT INTO gin_cohort VALUES(9,'{9}')")
	enginetest.SessionSQL(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2}')")
	enginetest.SessionSQL(t, a, "SAVEPOINT retained")
	enginetest.SessionSQL(t, c, "BEGIN")
	enginetest.SessionSQL(t, c, "INSERT INTO gin_cohort VALUES(2,'{3}')")
	enginetest.SessionSQL(t, c, "PREPARE TRANSACTION 'gin_cancel'")
	e.Mu.Lock()
	prepared := core.EnginePreparedRef(e).Lookup("gin_cancel")
	preparedWrites := prepared.Writes[target.SQLIndex.Parent]
	roots := maps.Clone(e.Storage.IndexRoots)
	e.Mu.Unlock()
	old := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
	a.Transactions.Mu.Lock()
	saved := a.Transactions.Savepoints[0].Writes[target.SQLIndex.Parent]
	revision := a.Transactions.GinRevision
	a.Transactions.Mu.Unlock()
	pause := util.NewExecutionPause("gin.maintenance.prepared")
	defer pause.Resume()
	ctx, cancel := context.WithCancel(util.WithExecutionPause(context.Background(), pause))
	defer cancel()
	await := enginetest.ExecuteAsync(t, e, b, ctx, "SELECT gin_clean_pending_list('gin_cohort_i'::regclass)")
	ginCoreAwaitPause(t, pause)
	cancel()
	pause.Resume()
	result, err := await()
	if result.Rows != nil {
		result.Rows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cleanup cancellation", err)
	}
	if !storage.SameVersionRoot(old, ginCoreWrites(a.Transactions, target.SQLIndex.Parent)) {
		t.Fatal("cancellation replaced active writes")
	}
	a.Transactions.Mu.Lock()
	unchanged := storage.SameVersionRoot(saved, a.Transactions.Savepoints[0].Writes[target.SQLIndex.Parent]) && revision == a.Transactions.GinRevision
	a.Transactions.Mu.Unlock()
	e.Mu.Lock()
	unchanged = unchanged && storage.SameVersionRoot(preparedWrites, prepared.Writes[target.SQLIndex.Parent]) && maps.Equal(roots, e.Storage.IndexRoots) && prepared.GinRevision == 0
	e.Mu.Unlock()
	if !unchanged {
		t.Fatal("cancellation partially installed cohort")
	}
	ginCoreClean(t, b)
	enginetest.SessionSQL(t, a, "ROLLBACK TO retained")
	enginetest.SessionSQL(t, a, "COMMIT")
	enginetest.SessionSQL(t, b, "COMMIT PREPARED 'gin_cancel'")
	ginCorePending(t, e, target, 0, 3)
}
func ginMaintenanceLiveReceipt(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FUNCTION gin_receipt(integer[]) RETURNS integer[] IMMUTABLE RETURN $1", "CREATE TABLE gin_cohort(id integer,a integer[])", "CREATE INDEX gin_cohort_i ON gin_cohort USING gin(gin_receipt(a))", "INSERT INTO gin_cohort VALUES(1,'{2}')", "BEGIN", "CREATE OR REPLACE FUNCTION gin_receipt(integer[]) RETURNS integer[] IMMUTABLE LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$"} {
		enginetest.SessionSQL(t, a, q)
	}
	enginetest.SessionSQL(t, a, "ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_receipt_array")
	target := ginCoreRelation(t, e, "gin_cohort_i")
	a.Transactions.Mu.Lock()
	old := a.Transactions.Catalog
	candidate := index.JournalIndexesOrZero(old).Candidates[target.Address()]
	a.Transactions.Mu.Unlock()
	if candidate.Root == nil {
		t.Fatal("routine replacement omitted genuine source receipt candidate")
	}
	ginCoreClean(t, b)
	a.Transactions.Mu.Lock()
	next := a.Transactions.Catalog
	nextCandidate := index.JournalIndexesOrZero(next).Candidates[target.Address()]
	a.Transactions.Mu.Unlock()
	e.Mu.Lock()
	current := e.Catalog.Current()
	roots, stores := e.Storage.IndexRoots, e.Storage.Relations
	e.Mu.Unlock()
	if next == old || storage.GinPendingIncidences(candidate.Root.Gin) == 0 || storage.GinPendingIncidences(nextCandidate.Root.Gin) != 0 || !index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{target.Address(): nextCandidate}, current, roots, stores) || index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{target.Address(): candidate}, current, roots, stores) {
		t.Fatal("live receipt not certified, or detached receipt accepted")
	}
	enginetest.SessionSQL(t, a, "COMMIT")
	ginCorePending(t, e, target, 0, 1)
}
func TestGINMaintenanceTargetAdmission(t *testing.T) {
	e, _, _, target := ginCoreFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	// This is diagnostic precedence with an explicit fact, NOT a real recovery
	// mode or public recovery oracle; the embedded runtime has no such mode.
	err = core.GinMaintenanceTarget(auth, target, true, op.Session.SessionToken)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != "recovery is in progress" || diagnostic.Hint != "GIN pending list cannot be cleaned up during recovery." {
		t.Fatal(err)
	}
	table := ginCoreRelation(t, e, "gin_cohort")
	err = core.GinMaintenanceTarget(auth, table, true, op.Session.SessionToken)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
		t.Fatal("index_open kind precedence", err)
	}
	if err := core.GinMaintenanceTarget(auth, target, false, op.Session.SessionToken); err != nil {
		t.Fatal(err)
	}
}

func ginMaintenancePresentationCohort(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "savepoint"} {
		t.Run(finish, func(t *testing.T) {
			e, a, b, target := ginCoreFixture(t)
			enginetest.SessionSQL(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2}')")
			enginetest.SessionSQL(t, a, "SAVEPOINT before_presentation")
			old := ginCoreWrites(a.Transactions, target.SQLIndex.Parent)
			enginetest.SessionSQL(t, a, "ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_cohort_array")
			enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(2,'{2,3}')")
			a.Transactions.Mu.Lock()
			e.Mu.Lock()
			journal := a.Transactions.Catalog
			view := e.PrivateCatalogLocked(journal)
			presented := view.MustRelation(target.Address())
			e.Mu.Unlock()
			a.Transactions.Mu.Unlock()
			ginCoreClean(t, b)
			ginCoreOutput(t, ginCoreWrites(a.Transactions, target.SQLIndex.Parent), presented, false)
			ginCoreOutput(t, old, target, true)
			if finish == "rollback" {
				enginetest.SessionSQL(t, a, "ROLLBACK")
				ginCorePending(t, e, target, 0, 0)
				return
			}
			if finish == "savepoint" {
				enginetest.SessionSQL(t, a, "ROLLBACK TO before_presentation")
				ginCoreOutput(t, ginCoreWrites(a.Transactions, target.SQLIndex.Parent), target, false)
			}
			enginetest.SessionSQL(t, a, "COMMIT")
			members := 2
			if finish == "savepoint" {
				members = 1
			}
			ginCorePending(t, e, ginCoreRelation(t, e, "gin_cohort_i"), 0, members)
		})
	}
}

func ginMaintenanceReturningStage(t *testing.T) {
	e, a, _, target := ginCoreFixture(t)
	pause := util.NewExecutionPause("gin.maintenance.prepared")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	op, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	statement := enginetest.Statement(t, "INSERT INTO gin_cohort VALUES(1,'{2,3}') RETURNING gin_clean_pending_list('gin_cohort_i'::regclass)>0")
	done := make(chan error, 1)
	joined := false
	go func() {
		result, err := scope.Execute(statement, 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		err = scope.Finish(err)
		op.Done()
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		pause.Resume()
		if !joined {
			<-done
		}
	})
	ginCoreAwaitPause(t, pause)
	produced := scope.Stage.Rows[target.SQLIndex.Parent]
	ginCoreOutput(t, produced, target, true)
	if len(produced) != 1 || produced[0].Version == 0 || len(produced[0].IndexOutputs[target.Address()].Keys) != 1 {
		t.Fatal("RETURNING reached before genuine index production")
	}
	keys := produced[0].IndexOutputs[target.Address()].Keys
	if keys[0].ArrayLen() != 2 {
		t.Fatal("producer did not retain actual typed key")
	}
	pause.Resume()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	root := ginCorePending(t, e, target, 0, 1)
	if &root.Entries[0].Keys[0] != &keys[0] || !produced[0].IndexOutputs[target.Address()].GinPending {
		t.Fatal("publication reran key recipe or mutated retained produced output")
	}
}
