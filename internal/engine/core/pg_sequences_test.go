package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgSequencesRows(t *testing.T, s *session.Session, query string, want [][]driver.Value) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, query)
	defer result.Rows.Close()
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", query, got, want)
	}
}

func TestPGSequencesDescriptors(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048}
	d := e.Catalog.State.MustRelation(address)
	if !d.VirtualView || d.RowType != 12050 || len(d.Columns) != 11 {
		t.Fatal(d)
	}
	names := []string{"schemaname", "sequencename", "sequenceowner", "data_type", "start_value", "min_value", "max_value", "increment_by", "cycle", "cache_size", "last_value"}
	typeIDs := []sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Regtype, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Bool, sqltype.Int8, sqltype.Int8}
	for i, column := range d.Columns {
		if column.Name != names[i] || column.Usage.Type.ID() != typeIDs[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 12048 || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) {
			t.Fatal(i, column)
		}
		if i < 3 && column.Usage.Collation.ID != 950 {
			t.Fatal("NAME collation", column)
		}
	}
	row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12050})
	array := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12049})
	if row.OwningRelation != address || row.Array != 12049 || array.OwningRelation != address || array.SQLType.Element() != row.SQLType {
		t.Fatal(row, array)
	}
	if err := (core.RelationProvider).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
}

func TestPGSequencesProvider(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 10")
	enginetest.SessionSQL(t, a, "CREATE TEMP SEQUENCE temp_s")
	pgSequencesRows(t, a, "SELECT sequencename FROM pg_sequences ORDER BY sequencename", [][]driver.Value{{"s"}, {"temp_s"}})
	pgSequencesRows(t, b, "SELECT sequencename FROM pg_sequences", [][]driver.Value{{"s"}})
	sequenceScalar(t, a, "SELECT nextval('s')", 1)
	sequenceScalar(t, b, "SELECT nextval('s')", 11)
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	before := *e.Storage.Sequences[ref.Address]
	pgSequencesRows(t, a, "SELECT start_value,cache_size,last_value FROM pg_sequences WHERE sequencename='s'", [][]driver.Value{{int64(1), int64(10), int64(20)}})
	if *e.Storage.Sequences[ref.Address] != before {
		t.Fatal("provider changed generator")
	}
	sequenceScalar(t, a, "SELECT nextval('s')", 2)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE own_s AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CACHE 9 CYCLE")
	pgSequencesRows(t, a, "SELECT start_value,min_value,max_value,increment_by,cycle,cache_size,last_value FROM pg_sequences WHERE sequencename='own_s'", [][]driver.Value{{int64(15), int64(-20), int64(200), int64(7), true, int64(9), nil}})
	sequenceScalar(t, b, "SELECT count(*) FROM pg_sequences WHERE sequencename='own_s'", 0)
	enginetest.SessionSQL(t, a, "SAVEPOINT before_drop")
	enginetest.SessionSQL(t, a, "DROP SEQUENCE own_s")
	sequenceScalar(t, a, "SELECT count(*) FROM pg_sequences WHERE sequencename='own_s'", 0)
	enginetest.SessionSQL(t, a, "ROLLBACK TO before_drop")
	sequenceScalar(t, a, "SELECT count(*) FROM pg_sequences WHERE sequencename='own_s'", 1)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 42")
	pgSequencesRows(t, a, "SELECT last_value FROM pg_sequences WHERE sequencename='s'", [][]driver.Value{{nil}})
	enginetest.TransactionEnd(t, a, tx, false)
	pgSequencesRows(t, a, "SELECT last_value FROM pg_sequences WHERE sequencename='s'", [][]driver.Value{{int64(20)}})
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE stale")
	stale := catalogRef(t, e, e.CatalogRead(), "stale")
	tx = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	sequenceScalar(t, a, "SELECT count(*) FROM pg_sequence", 3)
	enginetest.SessionSQL(t, b, "DROP SEQUENCE stale")
	enginetest.SessionSQL(t, b, "CREATE SEQUENCE stale START 99")
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, mustLockStatement("SELECT sequencename FROM pg_sequences WHERE sequencename='stale'"), 0, nil)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("stale partial rows")
	}
	sequenceOwnerDiagnostic(t, err, "XX000", fmt.Sprintf("could not open relation with OID %d", stale.Address.ObjectID), "")
	enginetest.TransactionEnd(t, a, tx, false)
}

func TestPGSequencesACL(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	sequenceScalar(t, a, "SELECT setval('s',42)", 42)
	enginetest.SessionSQL(t, a, "CREATE ROLE reader")
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	owner := e.Catalog.State.MustRelation(ref.Address).Owner
	enginetest.SessionSQL(t, a, "SET SESSION AUTHORIZATION reader")
	for _, mask := range []authorization.Privileges{0, authorization.UpdatePrivilege, authorization.UsagePrivilege, authorization.SelectPrivilege, authorization.UsagePrivilege | authorization.UpdatePrivilege} {
		acl := authorization.ACLValue{Explicit: true}
		if mask != 0 {
			acl.Entries = []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: mask}}
		}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
		var value driver.Value
		if mask&(authorization.UsagePrivilege|authorization.SelectPrivilege) != 0 {
			value = int64(42)
		}
		pgSequencesRows(t, a, "SELECT sequencename,last_value FROM pg_sequences", [][]driver.Value{{"s", value}})
		reader, ok := e.Roles().Lookup("reader")
		if !ok {
			t.Fatal("reader absent")
		}
		acl = authorization.ACLValue{Explicit: true}
		if mask != 0 {
			acl.Entries = []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask}}
		}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
		pgSequencesRows(t, a, "SELECT last_value FROM pg_sequences", [][]driver.Value{{value}})
	}
}

func TestPGSequencesDemand(t *testing.T) {
	for _, query := range []string{
		"SELECT sequencename FROM pg_sequences WHERE sequencename='a'",
		"SELECT count(*) FROM pg_sequences WHERE sequencename='a'",
		"SELECT sequencename FROM v WHERE sequencename='a'",
		"SELECT s.sequencename FROM pg_sequences s JOIN pg_class c ON c.relname=s.sequencename WHERE s.sequencename='a'",
		"SELECT last_value FROM pg_sequences WHERE FALSE",
		"SELECT count(*) FROM pg_sequences",
		"SELECT sequencename FROM pg_sequences WHERE last_value IS NULL AND sequencename='a'",
		"SELECT sequencename FROM pg_sequences WHERE sequencename='a' FOR UPDATE",
		"SELECT last_value FROM pg_sequences WHERE FALSE FOR UPDATE",
		"SELECT sequencename FROM v WHERE sequencename='a' FOR UPDATE",
		"SELECT sequencename FROM marked WHERE sequencename='a'",
		"SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='a' FOR UPDATE",
		"SELECT s.sequencename FROM pg_sequences s JOIN t ON t.n=s.sequencename WHERE s.sequencename='a' FOR UPDATE OF s,t",
		"SELECT sequencename FROM joined WHERE sequencename='a' FOR UPDATE",
	} {
		t.Run(query, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE a")
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE b")
			enginetest.SessionSQL(t, a, "CREATE VIEW v AS SELECT * FROM pg_sequences")
			enginetest.SessionSQL(t, a, "CREATE VIEW marked AS SELECT * FROM pg_sequences FOR UPDATE")
			enginetest.SessionSQL(t, a, "CREATE TABLE t(n TEXT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES('a')")
			enginetest.SessionSQL(t, a, "CREATE VIEW joined AS SELECT s.sequencename FROM pg_sequences s JOIN t ON t.n=s.sequencename")
			tx := enginetest.TransactionBegin(t, a)
			sequencePreparedDescription(t, e, a, query)
			for _, name := range []string{"a", "b"} {
				ref := catalogRef(t, e, e.CatalogRead(), name)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				held := entry != nil && entry.Holders[a.Transactions.Ticket].Mode != 0
				if held {
					t.Fatal("Prepare invoked helper", name)
				}
			}
			result := enginetest.SessionSQL(t, a, query)
			result.Rows.Close()
			for _, name := range []string{"a", "b"} {
				ref := catalogRef(t, e, e.CatalogRead(), name)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				held := entry != nil && entry.Holders[a.Transactions.Ticket].Mode == lock.RowExclusive
				want := (name == "a" && query != "SELECT last_value FROM pg_sequences WHERE FALSE" && query != "SELECT last_value FROM pg_sequences WHERE FALSE FOR UPDATE") || query == "SELECT count(*) FROM pg_sequences"
				if held != want {
					t.Fatal(name, "RX", held, "want", want)
				}
			}
			tableRef := catalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.Locks.Inspect()
			tableMarked := false
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == tableRef && resource.Row != 0 && entry.Holders[a.Transactions.Ticket].Mode == lock.ForUpdate {
					tableMarked = true
				}
			}
			wantTableMark := query == "SELECT s.sequencename FROM pg_sequences s JOIN t ON t.n=s.sequencename WHERE s.sequencename='a' FOR UPDATE OF s,t" || query == "SELECT sequencename FROM joined WHERE sequencename='a' FOR UPDATE"
			if tableMarked != wantTableMark {
				t.Fatal("ordinary joined table rowmark", tableMarked, "want", wantTableMark)
			}
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestPGSequencesConcurrency(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
	tx := enginetest.TransactionBegin(t, a)
	result := enginetest.SessionSQL(t, a, "SELECT sequencename FROM pg_sequences")
	result.Rows.Close()
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	// RESTART uses the existing sequence DDL ShareRowExclusive admission,
	// which conflicts with the helper's transaction-owned RowExclusive lock.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := enginetest.LockAsync(e, b, ctx, "ALTER SEQUENCE s RESTART 19", false)
	done, finished := make(chan error, 1), make(chan struct{})
	go func() {
		done <- <-worker
		close(finished)
	}()
	active := true
	defer func() {
		cancel()
		<-finished
		if active {
			enginetest.TransactionEnd(t, a, tx, false)
		}
	}()
	ownedLockQueued(t, e, ref, lock.ShareRowExclusive, done)
	enginetest.TransactionEnd(t, a, tx, false)
	active = false
	enginetest.LockAwait(t, done, nil)
	<-finished
	pgSequencesRows(t, b, "SELECT last_value FROM pg_sequences", [][]driver.Value{{nil}})
	sequenceScalar(t, b, "SELECT nextval('s')", 19)
	enginetest.LockEmpty(t, e)
}

func TestPGSequencesCancellation(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 10")
	ref := catalogRef(t, e, e.CatalogRead(), "s")
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 19")
	before := *e.Storage.Sequences[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	done := enginetest.LockAsync(e, b, ctx, "SELECT sequencename FROM pg_sequences", false)
	ownedLockQueued(t, e, ref, lock.RowExclusive, done)
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	if *e.Storage.Sequences[ref.Address] != before {
		t.Fatal("canceled provider mutated generator")
	}
	enginetest.TransactionEnd(t, a, tx, false)
	pgSequencesRows(t, b, "SELECT last_value FROM pg_sequences", [][]driver.Value{{nil}})
	enginetest.LockEmpty(t, e)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	op, err := b.Acquire(canceled)
	if err == nil {
		op.Done()
		t.Fatal("precanceled acquire succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func sequenceOwnerDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func ownedLockQueued(t *testing.T, e *core.Engine, ref ids.ObjectRef, mode lock.Mode, done <-chan error) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var request *lock.Request
		if entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == mode {
			request = entry.Pending[0].Request
		}
		if request != nil {
			return request
		}
		select {
		case err := <-done:
			t.Fatalf("OWNED BY operation completed before required %d admission on %v: %v", mode, ref, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("required exact relation admission not observed")
		}
		runtime.Gosched()
	}
}
