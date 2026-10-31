package binder_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)


func TestPgstathashindexSimulation(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE hash_sample(k integer)", "INSERT INTO hash_sample VALUES(1),(1),(2),(NULL)", "CREATE INDEX hash_sample_i ON hash_sample USING hash(k)"} { enginetest.Run(t, e, q) }
	c := pgstatindexCaptureFixture(t, e, nil, "hash_sample_i")
	pgstatindexCaptureWant(t, c, 3, 0, 60)
	baseline := c
	root := *c.Root
	hash := *root.Hash
	// Independent bucket multiplicity witness; entry coordinates are not stats authority.
	hash.Buckets = map[uint64][]storage.HashEntry{7: {{Position: -9}, {Position: -9}}, 11: {{Position: 999}}}
	root.Hash = &hash
	c.Root = &root
	c.Writes = []storage.RowVersion{{}}
	pgstatindexCaptureWant(t, c, 3, 0, 60)
	hash.Buckets = nil
	pgstatindexCaptureWant(t, c, 0, 0, 0)
	root.Hash = nil
	if _, err := index.CapturePgstattupleStats(context.Background(), c); err == nil { t.Fatal("missing root accepted") }
	pgstatindexUnitInstalled(t, e)
	s := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, tx, false)
	enginetest.SessionSQL(t, s, "INSERT INTO hash_sample VALUES(9)")
	enginetest.SessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
	enginetest.SessionSQL(t, s, "SAVEPOINT hash_before")
	enginetest.SessionSQL(t, s, "DELETE FROM hash_sample")
	enginetest.SessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
	enginetest.SessionSQL(t, s, "ROLLBACK TO hash_before")
	enginetest.SessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
	if e.Storage.IndexRoots[baseline.Index.Address()] != baseline.Root { t.Fatal("statistics published a root") }
	pgstatindexCaptureWant(t, baseline, 3, 0, 60)
}

func TestPgstathashindexCaptureTargetChecks(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	for _, q := range []string{"CREATE TABLE hash_target(k integer)", "CREATE INDEX hash_target_i ON hash_target USING hash(k)"} { enginetest.Run(t, e, q) }
	target := pgstatindexCaptureFixture(t, e, nil, "hash_target_i").Index
	before := e.Catalog.State
	defer func() { e.Mu.Lock(); e.Catalog.PublishLocked(before); e.Mu.Unlock() }()
	for _, tc := range []struct { kind catalog.RelationKind; method ids.OID; code, message string }{
		{catalog.IndexRelation, 405, "55000", `index "hash_target_i" is not valid`},
		{catalog.IndexRelation, 403, "42809", `relation "hash_target_i" is not a hash index`},
		{catalog.TableRelation, 405, "42809", `relation "hash_target_i" is not a hash index`},
	} {
		state := catalog.CloneState(before)
		bad, definition := *target, *target.SQLIndex
		definition.Valid, definition.MethodOID = false, tc.method
		bad.SQLIndex, bad.RelationKind = &definition, tc.kind
		bad.ObjectRevision++
		state.SetDescriptor(bad.Address(), &bad)
		e.Mu.Lock(); e.Catalog.PublishLocked(state); e.Mu.Unlock()
		amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT pgstathashindex('hash_target_i')"), sqlstate.Error{Code: tc.code, Message: tc.message})
	}
}

func TestPgstathashindexCaptureCancellation(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE hash_cancel(k integer)", "CREATE INDEX hash_cancel_i ON hash_cancel USING hash(k)"} { enginetest.Run(t, e, q) }
	c := pgstatindexCaptureFixture(t, e, nil, "hash_cancel_i")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := index.CapturePgstattupleStats(ctx, c); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	pgstatindexCaptureWant(t, c, 0, 0, 0)
}


func pgstatindexCaptureFixture(t *testing.T, e *core.Engine, s *session.Session, name string) index.PgstattupleStatsCapture {
	t.Helper()
	var target *catalog.Relation
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.Name() == name && r.SQLIndex != nil {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatal("missing index", name)
	}
	parent := e.CatalogRead().MustRelation(target.SQLIndex.Parent)
	store := e.Storage.Relations[parent.Address()]
	var writes []storage.RowVersion
	var deletes *visibility.PrivateDeletes
	if s != nil {
		writes = s.Transactions.Writes[parent.Address()]
		deletes = s.Transactions.Deletes[parent.Address()]
	}
	capture := index.PgstattupleStatsCapture{Index: target, Parent: parent, Root: index.EffectiveRoot(e.Storage.IndexRoots, nil, target.Address()), Store: *store, Writes: writes, Deletes: deletes, Frontier: e.Visibility.CommitSeq()}
	capture.Store.IndexSource = store.IndexIdentity()
	return capture
}

func pgstatindexCaptureWant(t *testing.T, c index.PgstattupleStatsCapture, live, dead, weight int64) {
	t.Helper()
	before := c.Root
	versions := c.Store.Versions
	got, err := index.CapturePgstattupleStats(context.Background(), c)
	if err != nil || got.LiveItems != live || got.DeadItems != dead || got.Weight != weight {
		t.Fatalf("capture: %+v, %v; want live/dead/bytes %d/%d/%d", got, err, live, dead, weight)
	}
	if before != c.Root || !storage.SameVersionRoot(versions, c.Store.Versions) {
		t.Fatal("capture replaced borrowed authority")
	}
}

func pgstatindexUnitInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pgstattuple")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgstattuple"}))
}

func amcheckUnitDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing structured diagnostic: %v", err)
	}
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	if !reflect.DeepEqual(*diagnostic, want) {
		t.Fatalf("diagnostic\n got: %#v\nwant: %#v", *diagnostic, want)
	}
}

func amcheckUnitCall(t *testing.T, e *core.Engine, query string) error {
	t.Helper()
	result, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, query), nil)
	if err == nil && result.Rows != nil {
		result.Rows.Close()
	}
	return err
}