package engine

import (
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestAlterTypeUnreferencedViewSourceUsage(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	run(t, e, "INSERT INTO t VALUES(1,7)")
	run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	viewRef := roleTableRef(t, e, "v")
	old := e.state.descriptors[viewRef.Address]
	run(t, e, "ALTER TABLE t ALTER a TYPE TEXT USING a")
	if e.state.descriptors[viewRef.Address] != old {
		t.Fatal("rewrote unrelated view definition")
	}
	if rows := tuples(run(t, e, "SELECT b FROM v")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(7)}}) {
		t.Fatal(rows)
	}
	if run(t, e, "SELECT b FROM v").Rows.Schema()[0].Usage.Type.ID() != 23 {
		t.Fatal("changed view result type")
	}
}
