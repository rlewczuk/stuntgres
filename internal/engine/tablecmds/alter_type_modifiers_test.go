package tablecmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestAlterTypeModifiers(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(c VARCHAR(5),n NUMERIC(8,3),stamp TIMESTAMP(6))")
	enginetest.Run(t, e, "INSERT INTO t VALUES('é🙂x',1.235,'2024-01-01 00:00:00.123456')")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	enginetest.Run(t, e, `ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."POSIX" USING c::varchar(2), ALTER n TYPE NUMERIC(5,2), ALTER stamp TYPE TIMESTAMP(2)`)
	next := e.Catalog.State.MustRelation(ref.Address)
	schema := enginetest.Run(t, e, "SELECT * FROM t").Rows.Schema()
	if schema[0].Usage.Type.ID() != 1043 || schema[0].Usage.Typmod != 6 || schema[0].Usage.Collation.ID != 951 || schema[1].Usage.Type.ID() != 1700 || schema[1].Usage.Typmod != (5<<16|2)+4 || schema[2].Usage.Type.ID() != 1114 || schema[2].Usage.Typmod != 2 {
		t.Fatal(schema)
	}
	if !reflect.DeepEqual(old.Attributes, next.Attributes) || old.Columns[0].Usage.Typmod != 9 {
		t.Fatal("changed original descriptor", old, next)
	}
	cursor := enginetest.IndependentFinalRows(enginetest.Run(t, e, "SELECT c,n FROM t").Rows)
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
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(c INT DEFAULT 7)")
	enginetest.Run(t, e, "INSERT INTO t DEFAULT VALUES")
	ref := enginetest.RoleTableRef(t, e, "t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	enginetest.Run(t, e, "ALTER TABLE t ALTER c TYPE INT")
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
		t.Fatal("identity TYPE copied definition/data")
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER c TYPE INT USING c+1")
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] == storage {
		t.Fatal("same definition USING failed to rewrite only data")
	}
	if rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT c FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(8)}}) {
		t.Fatal(rows)
	}
}

func TestAlterTypeStableDefaultDeferredAndReplay(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET TIME ZONE 'UTC'")
	enginetest.SessionSQL(t, s, "CREATE TABLE d(id INT,c TIMESTAMPTZ(6) DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL))")
	enginetest.SessionSQL(t, s, "INSERT INTO d(id) VALUES(1)")
	ref := enginetest.RoleTableRef(t, e, "d")
	before := e.Catalog.State.MustRelation(ref.Address)
	if before.ColumnMeta.Defaults[before.Attributes[1]].Expression.Properties().Volatility != expr.Stable {
		t.Fatal("fixture default is not Stable")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE d ALTER c TYPE TIMESTAMPTZ(2)")
	after := e.Catalog.State.MustRelation(ref.Address)
	if after.Columns[1].Usage.Typmod != 2 || after.ColumnMeta.Defaults[after.Attributes[1]].Expression.Properties().Volatility != expr.Stable {
		t.Fatal("TYPE froze stable default", after)
	}
	enginetest.SessionSQL(t, s, "SET TIME ZONE 'America/New_York'")
	enginetest.SessionSQL(t, s, "INSERT INTO d(id) VALUES(2)")
	want := [][]driver.Value{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 16:00:00"}}
	if rows := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id,(c AT TIME ZONE 'UTC')::text AS utc FROM d ORDER BY id")); !reflect.DeepEqual(rows, want) {
		t.Fatal(rows)
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "d").Address)
	if restored.Columns[1].Usage.Typmod != 2 || restored.ColumnMeta.Defaults[restored.Attributes[1]].Expression.Properties().Volatility != expr.Stable {
		t.Fatal("Restore lost stable default or precision", restored)
	}
	enginetest.SessionSQL(t, s, "SET TIME ZONE 'UTC'")
	enginetest.SessionSQL(t, s, "INSERT INTO d(id) VALUES(3)")
	want = append(want, []driver.Value{int64(3), "2004-07-01 12:00:00"})
	if rows := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id,(c AT TIME ZONE 'UTC')::text AS utc FROM d ORDER BY id")); !reflect.DeepEqual(rows, want) {
		t.Fatal(rows)
	}
}
