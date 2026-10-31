package dump_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestNullOrderingAndSnapshotLifetime(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t (i INTEGER, s TEXT, b BOOLEAN)")
	enginetest.Run(t, e, "INSERT INTO t VALUES (2,'z',TRUE),(NULL,'A',FALSE),(1,NULL,NULL),(1,'a',TRUE)")
	asc := enginetest.Run(t, e, "SELECT i FROM t ORDER BY i")
	want := [][]driver.Value{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}
	if !reflect.DeepEqual(enginetest.Tuples(asc), want) {
		t.Fatalf("ASC rows: %v", enginetest.Tuples(asc))
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT i FROM t ORDER BY i DESC")); !reflect.DeepEqual(got, [][]driver.Value{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}) {
		t.Fatalf("DESC rows: %v", got)
	}
	if r := enginetest.Run(t, e, "SELECT * FROM t WHERE i = NULL"); r.Count != 0 {
		t.Fatal("NULL equality matched")
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT s FROM t ORDER BY s")); !reflect.DeepEqual(got, [][]driver.Value{{"A"}, {"a"}, {"z"}, {nil}}) {
		t.Fatalf("text order: %v", got)
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT b FROM t ORDER BY b")); !reflect.DeepEqual(got, [][]driver.Value{{false}, {true}, {true}, {nil}}) {
		t.Fatalf("bool order: %v", got)
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES (0,'new',FALSE)")
	if !reflect.DeepEqual(enginetest.Tuples(asc), want) {
		t.Fatal("insert changed snapshot")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE replacement (x TEXT);")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(enginetest.Tuples(asc), want) {
		t.Fatal("reset/restore changed snapshot")
	}
}
