package partitioncmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func rangePartitionQueuedSQL(t *testing.T, e *core.Engine, session *session.Session, query string) (func() error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	finished := enginetest.LockAsync(e, session, ctx, query, false)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("queued RANGE utility did not terminate")
			}
		}
	})
	return func() error {
		t.Helper()
		select {
		case err := <-finished:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("queued RANGE utility did not terminate")
			return nil
		}
	}, cancel
}

func TestRangePartitionConcurrencyDefaultWriterPublication(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			writer := enginetest.StartSession(t, e, nil)
			creator := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
			enginetest.SessionSQL(t, writer, "CREATE TABLE rest PARTITION OF root DEFAULT")
			transaction := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "INSERT INTO root VALUES(2)")
			query := "CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(1) TO(3)"
			finished, cancel := rangePartitionQueuedSQL(t, e, creator, query)
			defer cancel()
			enginetest.LockPending(t, e, 1)
			if len(rangePartitionRelation(t, e, "root").Partition.Children) != 1 {
				t.Fatal("queued child published before writer release")
			}
			enginetest.TransactionEnd(t, writer, transaction, commit)
			err := finished()
			if commit {
				var diagnostic *sqlstate.Error
				expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `updated partition constraint for default partition "rest" would be violated by some row`, Schema: "public", Table: "rest"}
				if !errors.As(err, &diagnostic) || *diagnostic != expected {
					t.Fatalf("committed default frontier: %#v / %v", diagnostic, err)
				}
				if len(rangePartitionRelation(t, e, "root").Partition.Children) != 1 {
					t.Fatal("failed DEFAULT validation published child")
				}
				if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, creator, "SELECT k FROM root")), [][]driver.Value{{int64(2)}}) {
					t.Fatal("failed DDL changed committed default row")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(rangePartitionRelation(t, e, "root").Partition.Children) != 2 {
					t.Fatal("rollback frontier did not permit child publication")
				}
				enginetest.SessionSQL(t, creator, "INSERT INTO root VALUES(2)")
				if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, creator, "SELECT k FROM bounded")), [][]driver.Value{{int64(2)}}) {
					t.Fatal("new bound did not route after serialized publication")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestRangePartitionConcurrencyCanceledDDLReleasesHierarchyLocks(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	waiter := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	enginetest.SessionSQL(t, holder, "CREATE TABLE rest PARTITION OF root DEFAULT")
	transaction := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "INSERT INTO rest VALUES(7)")
	query := "CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(0) TO(5)"
	finished, cancel := rangePartitionQueuedSQL(t, e, waiter, query)
	enginetest.LockPending(t, e, 1)
	cancel()
	if err := finished(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(rangePartitionRelation(t, e, "root").Partition.Children) != 1 {
		t.Fatal("canceled queued DDL published metadata")
	}
	enginetest.TransactionEnd(t, holder, transaction, false)
	enginetest.SessionSQL(t, waiter, query)
	enginetest.SessionSQL(t, waiter, "INSERT INTO root VALUES(1),(7)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, waiter, "SELECT k FROM root ORDER BY k")), [][]driver.Value{{int64(1)}, {int64(7)}}) {
		t.Fatal("canceled hierarchy utility poisoned later work")
	}
	enginetest.LockEmpty(t, e)
}

func TestRangePartitionConcurrencyOverlappingDefinitionsSerialize(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	first := enginetest.StartSession(t, e, nil)
	second := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	enginetest.SessionSQL(t, holder, "CREATE TABLE rest PARTITION OF root DEFAULT")
	transaction := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "INSERT INTO root VALUES(50)")
	firstQuery := "CREATE TABLE first_child PARTITION OF root FOR VALUES FROM(0) TO(10)"
	secondQuery := "CREATE TABLE second_child PARTITION OF root FOR VALUES FROM(5) TO(15)"
	firstDone, firstCancel := rangePartitionQueuedSQL(t, e, first, firstQuery)
	defer firstCancel()
	enginetest.LockPending(t, e, 1)
	secondDone, secondCancel := rangePartitionQueuedSQL(t, e, second, secondQuery)
	defer secondCancel()
	enginetest.LockPending(t, e, 2)
	enginetest.TransactionEnd(t, holder, transaction, false)
	if err := firstDone(); err != nil {
		t.Fatal(err)
	}
	err := secondDone()
	var diagnostic *sqlstate.Error
	expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: `partition "second_child" would overlap partition "first_child"`, Position: strconv.Itoa(strings.Index(secondQuery, "(5)") + 2)}
	if !errors.As(err, &diagnostic) || *diagnostic != expected {
		t.Fatalf("overlap after queued admission: %#v / %v", diagnostic, err)
	}
	root := rangePartitionRelation(t, e, "root")
	if len(root.Partition.Children) != 2 {
		t.Fatal("overlap race published both children")
	}
	enginetest.SessionSQL(t, first, "INSERT INTO root VALUES(5),(15)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, first, "SELECT k FROM first_child")), [][]driver.Value{{int64(5)}}) || !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, first, "SELECT k FROM rest")), [][]driver.Value{{int64(15)}}) {
		t.Fatal("serialized partition/default routes disagree")
	}
	enginetest.LockEmpty(t, e)
}
