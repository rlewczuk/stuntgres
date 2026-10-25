package partitioncmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestRangePartitionCaptureAllPhysicalPrefixes(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root FOR VALUES FROM(10) TO(20)",
		"INSERT INTO root VALUES(1),(11)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	retained := enginetest.RoleExecute(t, e, op, "SELECT k FROM root")
	if retained.Count != 2 {
		t.Fatal("virtual source failed cumulative cardinality", retained.Count)
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO root VALUES(2),(12)")
	if !reflect.DeepEqual(enginetest.Tuples(retained), [][]driver.Value{{int64(1)}, {int64(11)}}) {
		t.Fatal("retained root cursor recaptured a physical prefix")
	}
	fresh := enginetest.RoleExecute(t, e, op, "SELECT k FROM root ORDER BY k")
	if fresh.Count != 4 || !reflect.DeepEqual(enginetest.Tuples(fresh), [][]driver.Value{{int64(1)}, {int64(2)}, {int64(11)}, {int64(12)}}) {
		t.Fatal("fresh root source did not capture all leaf prefixes")
	}
}

func TestRangePartitionCaptureEmptyHierarchyCatalogAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	retained := enginetest.RoleExecute(t, e, op, "SELECT k FROM root")
	if retained.Count != 0 {
		t.Fatal("childless virtual source was not empty", retained.Count)
	}
	enginetest.RoleExecute(t, e, op, "CREATE TABLE leaf PARTITION OF root DEFAULT")
	enginetest.RoleExecute(t, e, op, "INSERT INTO root VALUES(1)")
	if got := enginetest.Tuples(retained); len(got) != 0 {
		t.Fatal("childless retained source recaptured later topology", got)
	}
	if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT k FROM root")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
		t.Fatal("fresh hierarchy did not use its captured catalog/data view", got)
	}
}

func TestRangePartitionPublicationMultiLeafWriteStageSavepoints(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE root(k INT,v INT NOT NULL,CONSTRAINT positive CHECK(v>0)) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root(v UNIQUE) FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root(v UNIQUE) FOR VALUES FROM(10) TO(20)",
	} {
		enginetest.SessionSQL(t, session, query)
	}
	transaction := enginetest.TransactionBegin(t, session)
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(1,10),(11,20)")
	enginetest.SessionSQL(t, session, "SAVEPOINT original")
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO root VALUES(2,21),(12,20)"), 0, nil)
	op.Done()
	var diagnostic *sqlstate.Error
	expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "high_v_key"`, Detail: "Key (v)=(20) already exists.", Schema: "public", Table: "high", Constraint: "high_v_key"}
	if !errors.As(err, &diagnostic) || *diagnostic != expected {
		t.Fatalf("late destination unique: %#v / %v", diagnostic, err)
	}
	enginetest.SessionSQL(t, session, "ROLLBACK TO original")
	want := [][]driver.Value{{int64(1), int64(10)}, {int64(11), int64(20)}}
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), want) {
		t.Fatal("late destination unique published earlier leaf")
	}
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	enginetest.SessionSQL(t, session, "ROLLBACK TO original")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), want) {
		t.Fatal("savepoint did not undo all physical groups")
	}
	// The failed and rolled-back unique maps must both permit the reused values.
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	enginetest.TransactionEnd(t, session, transaction, true)
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("one-stage multi-leaf commit lost rows")
	}
	next := enginetest.TransactionBegin(t, session)
	enginetest.SessionSQL(t, session, "INSERT INTO root VALUES(3,31),(13,32)")
	enginetest.TransactionEnd(t, session, next, false)
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.SessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("transaction rollback did not undo every routed leaf")
	}
	enginetest.LockEmpty(t, e)
}
