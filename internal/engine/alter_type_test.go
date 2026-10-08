package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterTypeMetadataAndAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT DEFAULT 7 CONSTRAINT nn NOT NULL,b TEXT,c INT)")
	run(t, e, "INSERT INTO t VALUES(7,'keep',9)")
	run(t, e, "COMMENT ON COLUMN t.a IS 'survivor'")
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	held := run(t, e, "SELECT * FROM t")
	run(t, e, "ALTER TABLE t ALTER a TYPE BIGINT USING a+1")
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	if next == old || next.address != old.address || next.owner != old.owner || !reflect.DeepEqual(next.attributes, old.attributes) || !reflect.DeepEqual(next.columnComments, old.columnComments) || !reflect.DeepEqual(next.notNullConstraints, old.notNullConstraints) {
		t.Fatal(old, next)
	}
	schema := run(t, e, "SELECT * FROM t").Rows.Schema()
	if schema[0].Usage.Type.ID() != 20 || schema[0].Usage.Typmod != -1 || schema[0].OriginAttribute != 1 || schema[0].OriginTable != uint32(ref.Address.ObjectID) || schema[0].Nullable {
		t.Fatal(schema)
	}
	if !reflect.DeepEqual(tuples(held), [][]driver.Value{{int64(7), "keep", int64(9)}}) || held.Rows.Schema()[0].Usage.Type.ID() != 23 {
		t.Fatal("mutated held result")
	}
	run(t, e, "INSERT INTO t(b,c) VALUES('new',10)")
	if got := tuples(run(t, e, "SELECT a FROM t ORDER BY c")); !reflect.DeepEqual(got, [][]driver.Value{{int64(8)}, {int64(7)}}) {
		t.Fatal(got)
	}
	run(t, e, "CREATE TABLE bad(c TEXT)")
	run(t, e, "INSERT INTO bad VALUES('41'),('oops')")
	bad := roleTableRef(t, e, "bad")
	descriptor, storage := e.state.descriptors[bad.Address], e.storage[bad.Address]
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE bad ALTER c TYPE INT USING c::int"), nil)
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Code != "22P02" || got.Message != `invalid input syntax for type integer: "oops"` {
		t.Fatal(err)
	}
	if e.state.descriptors[bad.Address] != descriptor || e.storage[bad.Address] != storage {
		t.Fatal("published failed conversion")
	}
	if rows := tuples(run(t, e, "SELECT * FROM bad")); !reflect.DeepEqual(rows, [][]driver.Value{{"41"}, {"oops"}}) {
		t.Fatal(rows)
	}
}

func TestAlterTypeCollationMetadata(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(c TEXT)")
	for _, tc := range []struct {
		name string
		oid  uint32
	}{{`pg_catalog."C"`, 950}, {`pg_catalog."POSIX"`, 951}, {`pg_catalog."default"`, 100}} {
		run(t, e, "ALTER TABLE t ALTER c TYPE TEXT COLLATE "+tc.name)
		c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]
		if uint32(c.Usage.Collation.ID) != tc.oid {
			t.Fatal(c, tc.oid)
		}
	}
	run(t, e, `ALTER TABLE t ALTER c TYPE TEXT COLLATE "C"`)
	run(t, e, "ALTER TABLE t ALTER c TYPE TEXT")
	if c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]; c.Usage.Collation.ID != 100 {
		t.Fatal(c)
	}
}
