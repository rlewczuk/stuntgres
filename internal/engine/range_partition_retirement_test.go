package engine

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// SQL is fixture construction only: assertions exercise private dependency
// staging, cloned transaction roots and atomic physical installation, not a
// second native copy of the public SQL lifecycle contract.
func rangePartitionRetirementFixture(t *testing.T, s *Session, commitFixture bool) {
	t.Helper()
	provider := "CREATE TEMP TABLE rp_retire_provider(v INT)"
	for _, q := range []string{
		provider,
		"INSERT INTO rp_retire_provider VALUES(9)",
		"CREATE SEQUENCE rp_retire_seq",
		"CREATE TABLE rp_retire_root(k REGCLASS,v INT DEFAULT nextval('rp_retire_seq')) PARTITION BY RANGE(COALESCE(k,'rp_retire_provider'::REGCLASS))",
		"ALTER SEQUENCE rp_retire_seq OWNED BY rp_retire_root.v",
		"CREATE TABLE rp_retire_mid PARTITION OF rp_retire_root DEFAULT PARTITION BY RANGE(v)",
		"CREATE TABLE rp_retire_leaf PARTITION OF rp_retire_mid(v UNIQUE) FOR VALUES FROM(MINVALUE) TO(2)",
		"CREATE TABLE rp_retire_other PARTITION OF rp_retire_mid(v UNIQUE) DEFAULT",
		"CREATE INDEX rp_retire_index ON rp_retire_leaf(v)",
		"CREATE INDEX rp_retire_other_index ON rp_retire_other(v)",
		"CREATE SEQUENCE rp_retire_leaf_seq OWNED BY rp_retire_leaf.v",
		"CREATE SEQUENCE rp_retire_other_seq OWNED BY rp_retire_other.v",
		"CREATE VIEW rp_retire_view AS SELECT v FROM rp_retire_root",
	} {
		sessionSQL(t, s, q)
	}
	if !commitFixture {
		sessionSQL(t, s, "DELETE FROM rp_retire_provider WHERE v=9")
		sessionSQL(t, s, "INSERT INTO rp_retire_root(k,v) VALUES(NULL,1),(NULL,2)")
	}
}

func rangePartitionRetirementDescriptors(t *testing.T, catalog CatalogRead) (*relationDescriptor, *relationDescriptor) {
	t.Helper()
	var provider, root *relationDescriptor
	for _, d := range catalog.Descriptors() {
		if r, ok := d.(*relationDescriptor); ok {
			switch r.name {
			case "rp_retire_provider":
				provider = r
			case "rp_retire_root":
				root = r
			}
		}
	}
	if provider == nil || root == nil || root.Partition == nil || root.Partition.keys[0].derived == nil {
		t.Fatal("missing actual provider/expression hierarchy")
	}
	captured := false
	for _, ref := range root.Partition.keys[0].derived.dependencies {
		captured = captured || ref.Address == provider.address
	}
	if !captured {
		t.Fatal("bound COALESCE REGCLASS key did not capture its real provider")
	}
	return provider, root
}

func TestRangePartitionRetirementNilScopeStage(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	rangePartitionRetirementFixture(t, s, false)
	base := e.CatalogRead()
	provider, root := rangePartitionRetirementDescriptors(t, base)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{provider.address})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range edges {
		found = found || edge.address == root.address && edge.relation == root && edge.address.SubID == 0
	}
	if !found {
		t.Fatal("real closure lacks whole-hierarchy dependency edge")
	}
	if err := stage.Remove(e.catalog.domain.Ref(base.Epoch(), provider.address)); err != nil {
		t.Fatal(err)
	}
	if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	if err := stageOwnedExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rp_retire_provider", "rp_retire_root", "rp_retire_mid", "rp_retire_leaf", "rp_retire_other", "rp_retire_leaf_v_key", "rp_retire_other_v_key", "rp_retire_index", "rp_retire_other_index", "rp_retire_seq", "rp_retire_leaf_seq", "rp_retire_other_seq", "rp_retire_view"} {
		found := false
		for a, d := range base.Descriptors() {
			if d.Name() == name {
				found = true
				if _, removed := stage.removals[a]; !removed {
					t.Fatalf("closure retained %s", name)
				}
			}
		}
		if !found {
			t.Fatalf("fixture omitted %s", name)
		}
	}
	if e.CatalogRead().state != base.state {
		t.Fatal("preparation partially published catalog")
	}
	for a := range stage.removals {
		if e.CatalogRead().Get(a) != base.Get(a) {
			t.Fatal("staging changed live closure descriptor", a)
		}
	}
}

func TestRangePartitionRetirementCommitPrivateRoots(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	rangePartitionRetirementFixture(t, s, true)
	// Permanent CREATE/index/view utilities are not admitted in a native
	// explicit transaction. Build those owned descriptors outside it, then
	// configure the provider lifetime through the real validated catalog seam.
	committed := e.CatalogRead()
	provider, _ := rangePartitionRetirementDescriptors(t, committed)
	originalAction := provider.OnCommit
	stage := e.BeginCatalogStage(committed)
	defer e.DiscardCatalogStage(stage)
	replacement := *provider
	replacement.OnCommit = parser.OnCommitDrop
	replacement.revision++
	if err := stage.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	if committed.Get(provider.address) != provider || provider.OnCommit != originalAction {
		t.Fatal("provider lifetime replacement mutated the published predecessor")
	}
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO rp_retire_root(k,v) VALUES(NULL,1),(NULL,2)")
	sessionSQL(t, s, "DELETE FROM rp_retire_provider WHERE v=9")
	owner := s.transactions
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.mu.Lock()
	base := e.privateCatalogLocked(owner.catalog)
	e.mu.Unlock()
	_, _ = rangePartitionRetirementDescriptors(t, base)
	writes, deletes, changes := owner.writes, owner.deletes, owner.catalog
	if deletes[provider.address] == nil {
		t.Fatal("fixture has no actual committed-provider deletion root")
	}
	beforeWrites := make(map[ids.ObjectAddress][]rowVersion, len(writes))
	for a, rows := range writes {
		beforeWrites[a] = append([]rowVersion(nil), rows...)
	}
	beforeDeletes := maps.Clone(deletes)
	leafWrites := 0
	for a, rows := range writes {
		if r, ok := base.Relation(a); ok && r.Membership != nil && len(rows) != 0 {
			leafWrites++
		}
	}
	if leafWrites != 2 {
		t.Fatalf("fixture has %d private routed leaf write roots, want two", leafWrites)
	}
	nextWrites, nextDeletes, next, err := e.prepareTemporaryCommit(op, ref, writes, deletes, changes)
	if err != nil {
		t.Fatal(err)
	}
	if next == changes || !reflect.DeepEqual(writes, beforeWrites) || !reflect.DeepEqual(deletes, beforeDeletes) || owner.catalog != changes {
		t.Fatal("retirement preparation mutated original transaction state")
	}
	removed := make(map[ids.ObjectAddress]bool)
	for a, d := range next.descriptors {
		if d != nil {
			continue
		}
		removed[a] = true
		if _, exists := nextWrites[a]; exists {
			t.Fatal("removed closure retained private writes", a)
		}
		if _, exists := nextDeletes[a]; exists {
			t.Fatal("removed closure retained private deletes", a)
		}
		if next.storage[a].replacement != nil || next.generators[a] != nil || next.uniqueRoots[a] != nil {
			t.Fatal("removed closure retained staged storage/generator/index", a)
		}
	}
	for a := range writes {
		if !removed[a] {
			t.Fatal("routed leaf was not removed", a)
		}
	}
	op.Done()
	transactionEnd(t, s, ref, true)
	for a := range removed {
		if e.catalog.state.Get(a) != nil || e.storage[a] != nil || e.indexRoots[a] != nil || e.sequences[a] != nil {
			t.Fatal("outer commit retained closure state", a)
		}
	}
	if owner.writes != nil || owner.deletes != nil || owner.catalog != nil {
		t.Fatal("outer commit retained transaction roots")
	}
}

func TestRangePartitionRetirementPhysicalState(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TEMP TABLE rp_retire_peer(v INT UNIQUE)")
	sessionSQL(t, peer, "INSERT INTO rp_retire_peer VALUES(91)")
	rangePartitionRetirementFixture(t, s, false)
	base := e.CatalogRead()
	provider, _ := rangePartitionRetirementDescriptors(t, base)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stageExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	if err := stageOwnedExpressionDependencies(context.Background(), stage, []ids.ObjectAddress{provider.address}, parser.DropCascade, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	peerDescriptors := make(map[ids.ObjectAddress]Descriptor)
	peerStorage := make(map[ids.ObjectAddress]*relationStorage)
	peerIndexes := make(map[ids.ObjectAddress]*logicalIndexRoot)
	for a, d := range base.Descriptors() {
		if ns, ok := base.Namespace(d.Namespace()); ok && ns.Temporary == peer.token {
			peerDescriptors[a], peerStorage[a], peerIndexes[a] = d, e.storage[a], e.indexRoots[a]
		}
	}
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	for a := range stage.removals {
		if e.catalog.state.Get(a) != nil || e.storage[a] != nil || e.indexRoots[a] != nil || e.sequences[a] != nil {
			t.Fatal("physical retirement retained closure state", a)
		}
	}
	if e.catalog.state.Get(provider.address) != nil || e.storage[provider.address] != nil {
		t.Fatal("physical retirement retained provider")
	}
	for a, d := range peerDescriptors {
		if e.catalog.state.Get(a) != d || e.storage[a] != peerStorage[a] || e.indexRoots[a] != peerIndexes[a] {
			t.Fatal("physical retirement changed peer authority/storage/index", a)
		}
	}
}
