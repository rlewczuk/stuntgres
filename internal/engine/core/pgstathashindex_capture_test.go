package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"testing"
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
	s := sessionStart(t, e, nil)
	tx := transactionBegin(t, s)
	defer transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "INSERT INTO hash_sample VALUES(9)")
	sessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
	sessionSQL(t, s, "SAVEPOINT hash_before")
	sessionSQL(t, s, "DELETE FROM hash_sample")
	sessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
	sessionSQL(t, s, "ROLLBACK TO hash_before")
	sessionSQL(t, s, "SELECT pgstathashindex('hash_sample_i')")
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
