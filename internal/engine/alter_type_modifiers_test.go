package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterTypeModifiers(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(c VARCHAR(5),n NUMERIC(8,3),stamp TIMESTAMP(6))")
	run(t, e, "INSERT INTO t VALUES('é🙂x',1.235,'2024-01-01 00:00:00.123456')")
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	run(t, e, `ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."POSIX" USING c::varchar(2), ALTER n TYPE NUMERIC(5,2), ALTER stamp TYPE TIMESTAMP(2)`)
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	schema := run(t, e, "SELECT * FROM t").Rows.Schema()
	if schema[0].Usage.Type.ID() != 1043 || schema[0].Usage.Typmod != 6 || schema[0].Usage.Collation.ID != 951 || schema[1].Usage.Type.ID() != 1700 || schema[1].Usage.Typmod != (5<<16|2)+4 || schema[2].Usage.Type.ID() != 1114 || schema[2].Usage.Typmod != 2 {
		t.Fatal(schema)
	}
	if !reflect.DeepEqual(old.attributes, next.attributes) || old.columns[0].Usage.Typmod != 9 {
		t.Fatal("changed original descriptor", old, next)
	}
	cursor := independentFinalRows(run(t, e, "SELECT c,n FROM t").Rows)
	defer cursor.Close()
	values := make([]sqltype.Value, 2)
	present, err := cursor.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	for i, want := range []string{"é🙂", "1.24"} {
		got, err := values[i].Type().Output(context.Background(), values[i])
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	if present, err := cursor.Next(context.Background(), values); err != nil || present {
		t.Fatal(present, err)
	}
}

func TestAlterTypeIdentityRetainsStorageAndRevision(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(c INT DEFAULT 7)")
	run(t, e, "INSERT INTO t DEFAULT VALUES")
	ref := roleTableRef(t, e, "t")
	old, storage := e.state.descriptors[ref.Address], e.storage[ref.Address]
	run(t, e, "ALTER TABLE t ALTER c TYPE INT")
	if e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage {
		t.Fatal("identity TYPE copied definition/data")
	}
	run(t, e, "ALTER TABLE t ALTER c TYPE INT USING c+1")
	if e.state.descriptors[ref.Address] != old || e.storage[ref.Address] == storage {
		t.Fatal("same definition USING failed to rewrite only data")
	}
	if rows := tuples(run(t, e, "SELECT c FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(8)}}) {
		t.Fatal(rows)
	}
}
