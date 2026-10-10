package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRangePartitionCaptureAllPhysicalPrefixes(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root FOR VALUES FROM(10) TO(20)",
		"INSERT INTO root VALUES(1),(11)",
	} {
		roleExecute(t, e, op, query)
	}
	retained := roleExecute(t, e, op, "SELECT k FROM root")
	if retained.Count != 2 {
		t.Fatal("virtual source failed cumulative cardinality", retained.Count)
	}
	roleExecute(t, e, op, "INSERT INTO root VALUES(2),(12)")
	if !reflect.DeepEqual(tuples(retained), [][]driver.Value{{int64(1)}, {int64(11)}}) {
		t.Fatal("retained root cursor recaptured a physical prefix")
	}
	fresh := roleExecute(t, e, op, "SELECT k FROM root ORDER BY k")
	if fresh.Count != 4 || !reflect.DeepEqual(tuples(fresh), [][]driver.Value{{int64(1)}, {int64(2)}, {int64(11)}, {int64(12)}}) {
		t.Fatal("fresh root source did not capture all leaf prefixes")
	}
}

func TestRangePartitionCaptureEmptyHierarchyCatalogAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	retained := roleExecute(t, e, op, "SELECT k FROM root")
	if retained.Count != 0 {
		t.Fatal("childless virtual source was not empty", retained.Count)
	}
	roleExecute(t, e, op, "CREATE TABLE leaf PARTITION OF root DEFAULT")
	roleExecute(t, e, op, "INSERT INTO root VALUES(1)")
	if got := tuples(retained); len(got) != 0 {
		t.Fatal("childless retained source recaptured later topology", got)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT k FROM root")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
		t.Fatal("fresh hierarchy did not use its captured catalog/data view", got)
	}
}

func TestRangePartitionPublicationMultiLeafWriteStageSavepoints(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE root(k INT,v INT NOT NULL,CONSTRAINT positive CHECK(v>0)) PARTITION BY RANGE(k)",
		"CREATE TABLE low PARTITION OF root(v UNIQUE) FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE high PARTITION OF root(v UNIQUE) FOR VALUES FROM(10) TO(20)",
	} {
		sessionSQL(t, session, query)
	}
	transaction := transactionBegin(t, session)
	sessionSQL(t, session, "INSERT INTO root VALUES(1,10),(11,20)")
	sessionSQL(t, session, "SAVEPOINT original")
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "INSERT INTO root VALUES(2,21),(12,20)"), 0, nil)
	op.Done()
	var diagnostic *sqlstate.Error
	expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "high_v_key"`, Detail: "Key (v)=(20) already exists.", Schema: "public", Table: "high", Constraint: "high_v_key"}
	if !errors.As(err, &diagnostic) || *diagnostic != expected {
		t.Fatalf("late destination unique: %#v / %v", diagnostic, err)
	}
	sessionSQL(t, session, "ROLLBACK TO original")
	want := [][]driver.Value{{int64(1), int64(10)}, {int64(11), int64(20)}}
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), want) {
		t.Fatal("late destination unique published earlier leaf")
	}
	sessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	sessionSQL(t, session, "ROLLBACK TO original")
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), want) {
		t.Fatal("savepoint did not undo all physical groups")
	}
	// The failed and rolled-back unique maps must both permit the reused values.
	sessionSQL(t, session, "INSERT INTO root VALUES(2,21),(12,22)")
	transactionEnd(t, session, transaction, true)
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("one-stage multi-leaf commit lost rows")
	}
	next := transactionBegin(t, session)
	sessionSQL(t, session, "INSERT INTO root VALUES(3,31),(13,32)")
	transactionEnd(t, session, next, false)
	if !reflect.DeepEqual(tuples(sessionSQL(t, session, "SELECT k,v FROM root ORDER BY k")), [][]driver.Value{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(11), int64(20)}, {int64(12), int64(22)}}) {
		t.Fatal("transaction rollback did not undo every routed leaf")
	}
	lockEmpty(t, e)
}
