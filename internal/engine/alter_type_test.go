package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
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
	if next == old || next.address != old.address || next.owner != old.owner || !reflect.DeepEqual(next.attributes, old.attributes) || !reflect.DeepEqual(next.columnComments, old.columnComments) || len(next.notNullConstraints) != len(old.notNullConstraints) {
		t.Fatal(old, next)
	}
	beforeNN, afterNN := old.notNullConstraints[0], next.notNullConstraints[0]
	if afterNN.OID == 0 || afterNN.OID == beforeNN.OID || afterNN.Name != beforeNN.Name || afterNN.Attribute != beforeNN.Attribute || afterNN.NotValid != beforeNN.NotValid || beforeNN.Name != "nn" || beforeNN.NotValid {
		t.Fatal("TYPE failed to rebuild the same valid logical NN definition", beforeNN, afterNN)
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

func TestAlterTypeOrderedTransformsAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, declaration, seed, action, code, message string }{
		{"identity-first", "c INT", "(7)", "ALTER c TYPE INT USING 10/(c-c), ALTER c TYPE BIGINT USING c", "22012", "division by zero"},
		{"collation-first", "c TEXT", "('7')", `ALTER c TYPE TEXT COLLATE "C" USING (10/(c::int-c::int))::text, ALTER c TYPE VARCHAR(5) COLLATE "POSIX" USING c`, "22012", "division by zero"},
		{"action-order", "a TEXT,b TEXT", "('badA','badB')", "ALTER b TYPE INT USING b::int, ALTER a TYPE INT USING a::int", "22P02", `invalid input syntax for type integer: "badB"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t("+tc.declaration+")")
			run(t, e, "INSERT INTO t VALUES"+tc.seed)
			ref := roleTableRef(t, e, "t")
			old, store := e.state.descriptors[ref.Address], e.storage[ref.Address]
			wantRows := tuples(run(t, e, "SELECT * FROM t"))
			_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t "+tc.action), nil)
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != store {
				t.Fatal(got, err, "failed ordered list published descriptor/storage")
			}
			if rows := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(rows, wantRows) {
				t.Fatal(rows, wantRows)
			}
		})
	}
}

func TestAlterTypeCommittedOwnDMLProvenance(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT,c TEXT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,'bad'),(2,'deletedbad'),(3,'9')")
	ref := roleTableRef(t, e, "t")
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET c='7' WHERE id=1")
	sessionSQL(t, s, "DELETE FROM t WHERE id=2")
	if rows := s.transactions.writes[ref.Address]; len(rows) != 1 || rows[0].base == 0 || s.transactions.deletes[ref.Address] == nil {
		t.Fatal("fixture missed committed update/delete provenance", rows)
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER c TYPE INT USING c::int")
	if rows := s.transactions.writes[ref.Address]; len(rows) != 1 || rows[0].base != 0 || s.transactions.deletes[ref.Address] != nil {
		t.Fatal("rewrite retained omitted committed predecessor", rows)
	}
	transactionEnd(t, s, tx, true)
	if rows := tuples(sessionSQL(t, s, "SELECT id,c FROM t ORDER BY id")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(1), int64(7)}, {int64(3), int64(9)}}) {
		t.Fatal(rows)
	}
}

func TestAlterTypeCollationMetadata(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(c TEXT)")
	ref := roleTableRef(t, e, "t")
	run(t, e, `ALTER TABLE t ALTER c TYPE TEXT COLLATE "C"`)
	if c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]; c.Usage.Collation.ID != 950 {
		t.Fatal(c)
	}
	run(t, e, "INSERT INTO t VALUES(NULL),(NULL)")
	for _, tc := range []struct {
		name string
		oid  uint32
	}{{`pg_catalog."C"`, 950}, {`pg_catalog."POSIX"`, 951}, {`pg_catalog."default"`, 100}} {
		run(t, e, "ALTER TABLE t ALTER c TYPE TEXT COLLATE "+tc.name)
		c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]
		wantUsage := c.Usage.Type.Usage()
		wantUsage.Collation.ID = tc.oid
		if c.Usage != wantUsage || c.Usage.Type.ID() != 25 || c.OriginAttribute != 1 || c.OriginTable != uint32(ref.Address.ObjectID) {
			t.Fatal(c, tc.oid)
		}
		if rows := tuples(run(t, e, "SELECT c FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{nil}, {nil}}) {
			t.Fatal(rows)
		}
	}
	run(t, e, `ALTER TABLE t ALTER c TYPE TEXT COLLATE "C"`)
	run(t, e, `ALTER TABLE t ALTER c TYPE TEXT COLLATE "POSIX", ALTER c TYPE VARCHAR(5) COLLATE "C"`)
	if c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]; c.Usage.Collation.ID != 950 || c.Usage.Typmod != 9 {
		t.Fatal(c)
	}
	run(t, e, "ALTER TABLE t ALTER c TYPE TEXT")
	if c := run(t, e, "SELECT c FROM t").Rows.Schema()[0]; c.Usage.Collation.ID != 100 {
		t.Fatal(c)
	}
}

func TestAlterTypeCollationSchemaAuthorization(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER alter_reader")
	run(t, e, "CREATE SCHEMA denied")
	schema := e.state.names[NameKey{ClassID: NamespaceClassID, Name: "denied"}]
	if err := e.SetObjectACL(context.Background(), ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: schema}, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET SESSION AUTHORIZATION alter_reader")
	withPublicSchemaUnitOwner(t, e, "alter_reader", func() {
		sessionSQL(t, s, "CREATE TABLE t(c TEXT)")
	})
	sessionSQL(t, s, "INSERT INTO t VALUES('keep')")
	ref := roleTableRef(t, e, "t")
	old, storage := e.state.descriptors[ref.Address], e.storage[ref.Address]
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, `ALTER TABLE t ALTER c TYPE TEXT COLLATE denied."C"`), 0, nil)
	var got *sqlstate.Error
	op.Done()
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema denied", Position: "33"}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage {
		t.Fatal(got, err)
	}
	if rows := tuples(sessionSQL(t, s, "SELECT c FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{"keep"}}) {
		t.Fatal(rows)
	}
}
