package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	"testing"
)

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
func TestPgstatindexCapture(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	for _, q := range []string{"CREATE TABLE stats_heap(n integer,s text)", "INSERT INTO stats_heap VALUES(1,'A'),(2,'B'),(3,'C')", "CREATE INDEX stats_i ON stats_heap(lower(s)) INCLUDE(n) WHERE n>1"} {
		enginetest.Run(t, e, q)
	}
	baseline := pgstatindexCaptureFixture(t, e, nil, "stats_i")
	if len(baseline.Root.Entries) != 2 || len(baseline.Store.Versions) != 3 {
		t.Fatal("fixture lacks real partial memberships")
	}
	pgstatindexCaptureWant(t, baseline, 2, 0, 120)
	for _, damage := range []string{"position", "duplicate", "version"} {
		broken := baseline
		root := *baseline.Root
		root.Entries = append([]storage.IndexEntry(nil), baseline.Root.Entries...)
		switch damage {
		case "position":
			root.Entries[0].Position = len(baseline.Store.Versions)
		case "duplicate":
			root.Entries = append(root.Entries, root.Entries[0])
		case "version":
			root.Entries[0].Version++
		}
		broken.Root = &root
		if _, err := index.CapturePgstattupleStats(context.Background(), broken); err == nil {
			t.Fatal("invalid actual association accepted", damage)
		}
	}
	s := sessionStart(t, e, nil)
	tx := transactionBegin(t, s)
	defer transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "INSERT INTO stats_heap VALUES(4,'D'),(0,'E')")
	inserted := pgstatindexCaptureFixture(t, e, s, "stats_i")
	members, nonmembers := 0, 0
	for _, r := range inserted.Writes {
		out, ok := r.IndexOutputs[baseline.Index.Address()]
		if !ok || !index.OutputMatches(out, baseline.Index) {
			t.Fatal("missing actual insertion output")
		}
		if out.Member {
			members++
		} else {
			nonmembers++
		}
	}
	if members != 1 || nonmembers != 1 {
		t.Fatal(members, nonmembers)
	}
	pgstatindexCaptureWant(t, inserted, 3, 0, 180)
	sessionSQL(t, s, "SAVEPOINT stats_before")
	sessionSQL(t, s, "UPDATE stats_heap SET n=5,s='F' WHERE n=2")
	pgstatindexCaptureWant(t, pgstatindexCaptureFixture(t, e, s, "stats_i"), 3, 1, 240)
	sessionSQL(t, s, "DELETE FROM stats_heap WHERE n=3")
	pgstatindexCaptureWant(t, pgstatindexCaptureFixture(t, e, s, "stats_i"), 2, 2, 240)
	sessionSQL(t, s, "ROLLBACK TO stats_before")
	pgstatindexCaptureWant(t, pgstatindexCaptureFixture(t, e, s, "stats_i"), 3, 0, 180)
	if e.Storage.IndexRoots[baseline.Index.Address()] != baseline.Root {
		t.Fatal("private stats published/rebuilt baseline")
	}
	// Private required outputs are authority, not an invitation to reevaluate.
	broken := inserted
	broken.Writes = append([]storage.RowVersion(nil), inserted.Writes...)
	broken.Writes[0].IndexOutputs = maps.Clone(broken.Writes[0].IndexOutputs)
	delete(broken.Writes[0].IndexOutputs, baseline.Index.Address())
	if _, err := index.CapturePgstattupleStats(context.Background(), broken); err == nil {
		t.Fatal("missing producer output accepted")
	}
	broken = inserted
	broken.Writes = append([]storage.RowVersion(nil), inserted.Writes...)
	broken.Writes[0].IndexOutputs = maps.Clone(broken.Writes[0].IndexOutputs)
	out := broken.Writes[0].IndexOutputs[baseline.Index.Address()]
	foreign := *out.Index
	definition := *foreign.SQLIndex
	definition.Parent.ObjectID++
	foreign.SQLIndex = &definition
	out.Index = &foreign
	broken.Writes[0].IndexOutputs[baseline.Index.Address()] = out
	if _, err := index.CapturePgstattupleStats(context.Background(), broken); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("foreign output accepted", err)
	}
	// A candidate root that already contains an actual private version counts it once.
	represented := inserted
	root := *inserted.Root
	root.Entries = append([]storage.IndexEntry(nil), root.Entries...)
	for _, r := range inserted.Writes {
		out := r.IndexOutputs[baseline.Index.Address()]
		if out.Member {
			root.Entries = append(root.Entries, storage.IndexEntry{Row: r.Row, Version: r.Version, Position: -1, Keys: out.Keys})
		}
	}
	represented.Root = &root
	pgstatindexCaptureWant(t, represented, 3, 0, 180)
}

func TestPgstatindexCaptureCommittedAndSlots(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE stats_slots(k integer,n bigint,s text)", "INSERT INTO stats_slots VALUES(1,2,'x'),(NULL,NULL,NULL)", "CREATE INDEX slots_i ON stats_slots(k,n,s) INCLUDE(k)"} {
		enginetest.Run(t, e, q)
	}
	c := pgstatindexCaptureFixture(t, e, nil, "slots_i")
	// One full entry: 20+8+8+32+8; one all-NULL entry: 20.
	pgstatindexCaptureWant(t, c, 2, 0, 96)
	old := c.Root
	enginetest.Run(t, e, "DELETE FROM stats_slots WHERE k=1")
	c = pgstatindexCaptureFixture(t, e, nil, "slots_i")
	pgstatindexCaptureWant(t, c, 1, 0, 20)
	if len(old.Entries) != 2 || c.Root == old {
		t.Fatal("committed deletion mutated predecessor root")
	}
	enginetest.Run(t, e, "DELETE FROM stats_slots")
	pgstatindexCaptureWant(t, pgstatindexCaptureFixture(t, e, nil, "slots_i"), 0, 0, 0)
}

func TestPgstatindexCaptureTargetChecks(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE stats_target(n integer)")
	enginetest.Run(t, e, "CREATE INDEX stats_target_i ON stats_target(n)")
	target := pgstatindexCaptureFixture(t, e, nil, "stats_target_i").Index
	custom, _, _, _ := accessMethodClassUnit(t, e, "stats_alias")
	before := e.Catalog.State
	for _, tc := range []struct {
		kind          catalog.RelationKind
		method        ids.OID
		code, message string
	}{
		{catalog.IndexRelation, 403, "55000", `index "stats_target_i" is not valid`},
		{catalog.IndexRelation, 405, "42809", `relation "stats_target_i" is not a btree index`},
		{catalog.IndexRelation, custom.Address().ObjectID, "42809", `relation "stats_target_i" is not a btree index`},
		{catalog.TableRelation, 403, "42809", `relation "stats_target_i" is not a btree index`},
	} {
		state := catalog.CloneState(before)
		bad := *target
		definition := *target.SQLIndex
		definition.Valid = false
		definition.MethodOID = tc.method
		bad.SQLIndex = &definition
		bad.RelationKind = tc.kind
		bad.ObjectRevision++
		state.SetDescriptor(bad.Address(), &bad)
		e.Mu.Lock()
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT pgstatindex('stats_target_i')"), sqlstate.Error{Code: tc.code, Message: tc.message})
	}
	e.Mu.Lock()
	e.Catalog.PublishLocked(before)
	e.Mu.Unlock()
}

func TestPgstatindexCaptureTempAndOrder(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	for _, q := range []string{"CREATE TABLE stats_order(n integer)", "CREATE INDEX stats_order_i ON stats_order(n)", "CREATE SCHEMA stats_foreign"} {
		enginetest.Run(t, e, q)
	}
	c := pgstatindexCaptureFixture(t, e, nil, "stats_order_i")
	peer := sessionStart(t, e, nil)
	before := e.Catalog.State
	namespace := e.CatalogRead().MustNamespace(before.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "stats_foreign"}))
	for _, tc := range []struct {
		kind          catalog.RelationKind
		method        ids.OID
		partition     bool
		code, message string
	}{
		{catalog.IndexRelation, 403, false, "0A000", "cannot access temporary tables of other sessions"},
		{catalog.IndexRelation, 405, false, "42809", `relation "stats_order_i" is not a btree index`},
		{catalog.TableRelation, 403, false, "42809", `relation "stats_order_i" is not a btree index`},
		{catalog.IndexRelation, 403, true, "42809", `relation "stats_order_i" is not a btree index`},
	} {
		state := catalog.CloneState(before)
		ns := *namespace
		ns.Temporary = peer.SessionToken
		ns.ObjectRevision++
		state.SetDescriptor(ns.Address(), &ns)
		parent := *c.Parent
		parent.ObjectNamespace = ns.Address()
		parent.ObjectRevision++
		if tc.partition {
			parent.Partition = &catalog.PartitionDefinition{}
		}
		state.UnbindName(catalog.DescriptorNameKey(c.Parent))
		state.SetDescriptor(parent.Address(), &parent)
		state.BindName(catalog.DescriptorNameKey(&parent), parent.Address())
		target := *c.Index
		definition := *target.SQLIndex
		definition.Valid = false
		definition.MethodOID = tc.method
		target.SQLIndex = &definition
		target.RelationKind = tc.kind
		target.ObjectNamespace = ns.Address()
		target.ObjectRevision++
		state.UnbindName(catalog.DescriptorNameKey(c.Index))
		state.SetDescriptor(target.Address(), &target)
		state.BindName(catalog.DescriptorNameKey(&target), target.Address())
		e.Mu.Lock()
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		query := fmt.Sprintf("SELECT pgstatindex(%d::regclass)", target.Address().ObjectID)
		amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, query), sqlstate.Error{Code: tc.code, Message: tc.message})
	}
	e.Mu.Lock()
	e.Catalog.PublishLocked(before)
	e.Mu.Unlock()
	if e.Storage.IndexRoots[c.Index.Address()] != c.Root {
		t.Fatal("target validation changed captured root")
	}
}
