package partitioncmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestListPartitionPublicationCancellation(t *testing.T) {
	for _, target := range []string{"partition.evaluateListBounds", "partition.CheckDefaultRows", "(*Engine).ValidateCatalogStage"} {
		t.Run(target, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE TABLE root(k INT,v INT) PARTITION BY LIST(k)",
				"CREATE TABLE rest PARTITION OF root DEFAULT PARTITION BY LIST(v)",
				"CREATE TABLE deep PARTITION OF rest DEFAULT",
				"INSERT INTO root VALUES(7,70)",
			} {
				enginetest.SessionSQL(t, session, query)
			}
			before := e.CatalogRead().State()
			e.Mu.Lock()
			storageCount := len(e.Storage.Relations)
			e.Mu.Unlock()
			underlying, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &rangePartitionPhaseContext{Context: underlying, target: target, effect: cancel}
			op, err := session.Acquire(ctx.Context)
			if err != nil {
				t.Fatal(err)
			}
			ctx.Context, op.Ctx = op.Context(), ctx
			_, err = e.Execute(op, enginetest.Statement(t, "CREATE TABLE leaf PARTITION OF root(v UNIQUE) FOR VALUES IN(2,NULL)"), 0, nil)
			op.Done()
			if !ctx.entered.Load() || !errors.Is(err, context.Canceled) {
				t.Fatal("real definition phase did not cancel", target, ctx.entered.Load(), err)
			}
			if e.CatalogRead().State() != before {
				t.Fatal("canceled definition published catalog fragments")
			}
			e.Mu.Lock()
			unchanged := len(e.Storage.Relations) == storageCount
			e.Mu.Unlock()
			if !unchanged {
				t.Fatal("canceled definition installed leaf/index storage")
			}
			if got := enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7), int64(70)}}) {
				t.Fatal(got)
			}
			enginetest.SessionSQL(t, session, "CREATE TABLE leaf PARTITION OF root(v UNIQUE) FOR VALUES IN(2,NULL)")
			enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(2,20),(NULL,30)")
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("multileaf_apply", func(t *testing.T) {
		e := engine.New()
		session := enginetest.StartSession(t, e, nil)
		for _, query := range []string{
			"CREATE TABLE root(k INT,v INT) PARTITION BY LIST(k)",
			"CREATE TABLE low PARTITION OF root(v UNIQUE) FOR VALUES IN(1,2)",
			"CREATE TABLE high PARTITION OF root(v UNIQUE) FOR VALUES IN(11,12)",
			"INSERT INTO root VALUES(1,10),(11,20)",
		} {
			enginetest.SessionSQL(t, session, query)
		}
		underlying, cancel := context.WithCancel(context.Background())
		defer cancel()
		first, second := rangePartitionRelation(t, e, "low"), rangePartitionRelation(t, e, "high")
		op, err := session.Acquire(underlying)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		ctx := &partitionStagedCancelContext{Context: op.Context(), scope: scope, first: first.Address(), second: second.Address(), cancel: cancel}
		op.Ctx = ctx
		_, err = scope.Execute(enginetest.Statement(t, "INSERT INTO root VALUES(2,21),(12,22)"), 0, nil)
		err = scope.Finish(err)
		op.Done()
		if !ctx.entered.Load() || !errors.Is(err, context.Canceled) {
			t.Fatal("private first-leaf progress did not cancel", ctx.entered.Load(), err)
		}
		if got := enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(10)}, {int64(11), int64(20)}}) {
			t.Fatal("canceled INSERT published leaf groups", got)
		}
		enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
		if got := enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
			t.Fatal("canceled INSERT leaked provisional claims", got)
		}
		enginetest.LockEmpty(t, e)
	})
}

func TestListPartitionCancellationLockRelease(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	waiter := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "CREATE TABLE root(k INT) PARTITION BY LIST(k)")
	enginetest.SessionSQL(t, holder, "CREATE TABLE rest PARTITION OF root DEFAULT")
	transaction := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "INSERT INTO rest VALUES(7)")
	query := "CREATE TABLE leaf PARTITION OF root FOR VALUES IN(1,NULL)"
	finished, cancel := rangePartitionQueuedSQL(t, e, waiter, query)
	enginetest.LockPending(t, e, 1)
	cancel()
	if err := finished(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(rangePartitionRelation(t, e, "root").Partition.Children) != 1 {
		t.Fatal("canceled queued LIST definition published metadata")
	}
	enginetest.TransactionEnd(t, holder, transaction, false)
	enginetest.SessionSQL(t, waiter, query)
	enginetest.SessionSQL(t, waiter, "INSERT INTO root VALUES(1),(7),(NULL)")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, waiter, "SELECT k FROM root ORDER BY k NULLS LAST")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(7)}, {nil}}) {
		t.Fatal("canceled definition poisoned later routing", got)
	}
	enginetest.LockEmpty(t, e)
}
