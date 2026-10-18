package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func rangePartitionQueuedSQL(t *testing.T, e *Engine, session *Session, query string) (func() error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	finished := lockAsync(e, session, ctx, query, false)
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
			e := New()
			writer := sessionStart(t, e, nil)
			creator := sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
			sessionSQL(t, writer, "CREATE TABLE rest PARTITION OF root DEFAULT")
			transaction := transactionBegin(t, writer)
			sessionSQL(t, writer, "INSERT INTO root VALUES(2)")
			query := "CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(1) TO(3)"
			finished, cancel := rangePartitionQueuedSQL(t, e, creator, query)
			defer cancel()
			lockPending(t, e, 1)
			if len(rangePartitionRelation(t, e, "root").Partition.children) != 1 {
				t.Fatal("queued child published before writer release")
			}
			transactionEnd(t, writer, transaction, commit)
			err := finished()
			if commit {
				var diagnostic *sqlstate.Error
				expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `updated partition constraint for default partition "rest" would be violated by some row`, Schema: "public", Table: "rest"}
				if !errors.As(err, &diagnostic) || *diagnostic != expected {
					t.Fatalf("committed default frontier: %#v / %v", diagnostic, err)
				}
				if len(rangePartitionRelation(t, e, "root").Partition.children) != 1 {
					t.Fatal("failed DEFAULT validation published child")
				}
				if !reflect.DeepEqual(tuples(sessionSQL(t, creator, "SELECT k FROM root")), [][]driver.Value{{int64(2)}}) {
					t.Fatal("failed DDL changed committed default row")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(rangePartitionRelation(t, e, "root").Partition.children) != 2 {
					t.Fatal("rollback frontier did not permit child publication")
				}
				sessionSQL(t, creator, "INSERT INTO root VALUES(2)")
				if !reflect.DeepEqual(tuples(sessionSQL(t, creator, "SELECT k FROM bounded")), [][]driver.Value{{int64(2)}}) {
					t.Fatal("new bound did not route after serialized publication")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestRangePartitionConcurrencyCanceledDDLReleasesHierarchyLocks(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	waiter := sessionStart(t, e, nil)
	sessionSQL(t, holder, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	sessionSQL(t, holder, "CREATE TABLE rest PARTITION OF root DEFAULT")
	transaction := transactionBegin(t, holder)
	sessionSQL(t, holder, "INSERT INTO rest VALUES(7)")
	query := "CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(0) TO(5)"
	finished, cancel := rangePartitionQueuedSQL(t, e, waiter, query)
	lockPending(t, e, 1)
	cancel()
	if err := finished(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(rangePartitionRelation(t, e, "root").Partition.children) != 1 {
		t.Fatal("canceled queued DDL published metadata")
	}
	transactionEnd(t, holder, transaction, false)
	sessionSQL(t, waiter, query)
	sessionSQL(t, waiter, "INSERT INTO root VALUES(1),(7)")
	if !reflect.DeepEqual(tuples(sessionSQL(t, waiter, "SELECT k FROM root ORDER BY k")), [][]driver.Value{{int64(1)}, {int64(7)}}) {
		t.Fatal("canceled hierarchy utility poisoned later work")
	}
	lockEmpty(t, e)
}

func TestRangePartitionConcurrencyOverlappingDefinitionsSerialize(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	first := sessionStart(t, e, nil)
	second := sessionStart(t, e, nil)
	sessionSQL(t, holder, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	sessionSQL(t, holder, "CREATE TABLE rest PARTITION OF root DEFAULT")
	transaction := transactionBegin(t, holder)
	sessionSQL(t, holder, "INSERT INTO root VALUES(50)")
	firstQuery := "CREATE TABLE first_child PARTITION OF root FOR VALUES FROM(0) TO(10)"
	secondQuery := "CREATE TABLE second_child PARTITION OF root FOR VALUES FROM(5) TO(15)"
	firstDone, firstCancel := rangePartitionQueuedSQL(t, e, first, firstQuery)
	defer firstCancel()
	lockPending(t, e, 1)
	secondDone, secondCancel := rangePartitionQueuedSQL(t, e, second, secondQuery)
	defer secondCancel()
	lockPending(t, e, 2)
	transactionEnd(t, holder, transaction, false)
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
	if len(root.Partition.children) != 2 {
		t.Fatal("overlap race published both children")
	}
	sessionSQL(t, first, "INSERT INTO root VALUES(5),(15)")
	if !reflect.DeepEqual(tuples(sessionSQL(t, first, "SELECT k FROM first_child")), [][]driver.Value{{int64(5)}}) || !reflect.DeepEqual(tuples(sessionSQL(t, first, "SELECT k FROM rest")), [][]driver.Value{{int64(15)}}) {
		t.Fatal("serialized partition/default routes disagree")
	}
	lockEmpty(t, e)
}
