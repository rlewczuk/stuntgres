package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Independently project authoritative descriptors, then check both directions:
// exact keys and members, no stale/nonoperator addresses and no duplicates.
func assertOperatorIndex(t *testing.T, state *catalog.State) {
	t.Helper()
	want := make(map[catalog.OperatorNameKey]map[ids.ObjectAddress]bool)
	for address, descriptor := range state.Descriptors() {
		if operator, ok := descriptor.(*catalog.Operator); ok {
			key := catalog.OperatorNameKey{operator.Namespace(), operator.Name()}
			if want[key] == nil {
				want[key] = make(map[ids.ObjectAddress]bool)
			}
			want[key][address] = true
		}
	}
	if state.OperatorNames() == nil {
		t.Fatal("catalog root has no operator membership index")
	}
	if len(state.OperatorNames().Buckets) != len(want) {
		t.Fatalf("bucket keys: got %d, want %d", len(state.OperatorNames().Buckets), len(want))
	}
	for key, addresses := range state.OperatorNames().Buckets {
		members := want[key]
		if len(addresses) == 0 || len(addresses) != len(members) {
			t.Fatalf("bucket %v: got %v, want %v", key, addresses, members)
		}
		seen := make(map[ids.ObjectAddress]bool)
		for _, address := range addresses {
			if !members[address] || seen[address] {
				t.Fatalf("bucket %v contains stale, nonoperator or duplicate address %v", key, address)
			}
			seen[address] = true
		}
	}
	groups := regobj.CollectOperatorGroups(catalog.NewRead(nil, state))
	seen := make(map[ids.ObjectAddress]bool)
	for name, operators := range groups {
		for _, operator := range operators {
			if operator.Name() != name || state.Get(operator.Address()) != operator || seen[operator.Address()] {
				t.Fatal("alias groups do not expose exact current descriptors", operator)
			}
			seen[operator.Address()] = true
		}
	}
	count := 0
	for _, members := range want {
		count += len(members)
	}
	if len(seen) != count {
		t.Fatal("alias groups omitted operator membership")
	}
}

func indexUnitOperator(id ids.OID, namespace ids.ObjectAddress, name string) *catalog.Operator {
	return &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, Left: 23, Right: 23}
}

func TestUserOperatorIndexCopyOnWrite(t *testing.T) {
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}
	first := indexUnitOperator(90001, namespace, "#~#")
	other := indexUnitOperator(90002, namespace, "#?#")
	base := catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{first.Address(): first, other.Address(): other}, map[ids.NameKey]ids.ObjectAddress{catalog.DescriptorNameKey(first): first.Address(), catalog.DescriptorNameKey(other): other.Address()})
	base.SetOperatorNames(catalog.RebuildOperatorNames(base.Descriptors()))
	key := catalog.OperatorNameKey{namespace, first.Name()}
	// This unpublished fixture deliberately retains capacity and a sentinel
	// outside its length: append must not overwrite a sibling's backing array.
	backing := make([]ids.ObjectAddress, 4)
	backing[0], backing[1] = first.Address(), other.Address()
	base.OperatorNames().Buckets[key] = backing[:1]
	e := &Engine{Catalog: catalog.Store{State: base}}
	stage := e.BeginCatalogStage(e.Catalog.Read(base))
	second := indexUnitOperator(90003, namespace, first.Name())
	second.Left = 0
	if err := stage.Create(second); err != nil {
		t.Fatal(err)
	}
	child := e.StageRead(stage).State()
	sibling := catalog.CloneState(base)
	third := indexUnitOperator(90004, namespace, first.Name())
	third.Left, third.Right = 20, 20
	stage = e.BeginCatalogStage(e.Catalog.Read(sibling))
	if err := stage.Create(third); err != nil {
		t.Fatal(err)
	}
	siblingChild := e.StageRead(stage).State()
	if base.OperatorNames() != sibling.OperatorNames() || child.OperatorNames() == base.OperatorNames() || siblingChild.OperatorNames() == child.OperatorNames() {
		t.Fatal("root sharing/detachment is incorrect")
	}
	if backing[1] != other.Address() {
		t.Fatal("child append overwrote shared spare capacity")
	}
	untouched := catalog.OperatorNameKey{namespace, other.Name()}
	if &child.OperatorNames().Buckets[untouched][0] != &base.OperatorNames().Buckets[untouched][0] {
		t.Fatal("untouched bucket copied")
	}
	for _, state := range []*catalog.State{base, sibling, child, siblingChild} {
		assertOperatorIndex(t, state)
	}
	// Multiple removals/inserts in one bucket must neither duplicate members
	// nor mutate either predecessor, regardless of map iteration order.
	stage = e.BeginCatalogStage(e.Catalog.Read(child))
	moved := *first
	moved.ObjectNamespace.ObjectID++
	moved.ObjectName, moved.ObjectRevision = "#@#", 2
	if err := stage.Replace(&moved); err != nil {
		t.Fatal(err)
	}
	fourth := indexUnitOperator(90005, namespace, first.Name())
	fourth.Right = 20
	if err := stage.Create(fourth); err != nil {
		t.Fatal(err)
	}
	movedRoot := e.StageRead(stage).State()
	for _, state := range []*catalog.State{base, child, siblingChild, movedRoot} {
		assertOperatorIndex(t, state)
	}
}

func TestUserOperatorIndexCurrentMetadata(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	first := indexUnitOperator(90001, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "#~#")
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(first); err != nil {
		t.Fatal(err)
	}
	base = e.StageRead(stage)
	oldIndex := base.State().OperatorNames()
	for _, update := range []func(*catalog.Operator){
		func(d *catalog.Operator) { d.ObjectRevision++ },
		func(d *catalog.Operator) { d.Owner = e.RoleStore.Domain.Principal(10) },
		func(d *catalog.Operator) { d.Left, d.Right = 0, 20 },
		func(d *catalog.Operator) { d.Result, d.Function = 20, 463 },
		func(d *catalog.Operator) { d.Hash, d.Merge, d.Negator = true, true, 90002 },
	} {
		previous := base.MustOperator(first.Address())
		next := *previous
		update(&next)
		stage = e.BeginCatalogStage(base)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		current := e.StageRead(stage)
		if current.State().OperatorNames() != oldIndex {
			t.Fatal("metadata-only replacement copied membership")
		}
		oldGroups, newGroups := regobj.CollectOperatorGroups(base), regobj.CollectOperatorGroups(current)
		if oldGroups[first.Name()][0] != previous || newGroups[first.Name()][0] != &next || base.Get(first.Address()) != previous {
			t.Fatal("shared membership froze metadata or changed predecessor")
		}
		assertOperatorIndex(t, base.State())
		assertOperatorIndex(t, current.State())
		base = current
	}
	// A nonoperator stage must retain the exact membership root too.
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90010}, ObjectName: "unrelated", ObjectRevision: 1}
	stage = e.BeginCatalogStage(base)
	if err := stage.Create(namespace); err != nil {
		t.Fatal(err)
	}
	unrelated := e.StageRead(stage).State()
	if unrelated.OperatorNames() != oldIndex {
		t.Fatal("unrelated stage copied membership")
	}
	assertOperatorIndex(t, unrelated)
}

func TestUserOperatorIndexPublicationAndOverlay(t *testing.T) {
	e := New()
	before := e.CatalogRead()
	d := indexUnitOperator(90001, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "#~#")
	d.Owner, d.Result, d.Function = e.RoleStore.Domain.Principal(10), 23, 177
	stage := e.BeginCatalogStage(before)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.StageRead(stage).State())
	collationUnitPublish(t, e, stage)
	published := e.CatalogRead()
	assertOperatorIndex(t, published.State())
	assertOperatorIndex(t, before.State())
	// Real publication of a namespace move, not just editor-helper assertions.
	moved := *d
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	moved.ObjectRevision++
	stage = e.BeginCatalogStage(published)
	if err := stage.Replace(&moved); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.StageRead(stage).State())
	collationUnitPublish(t, e, stage)
	current := e.CatalogRead()
	assertOperatorIndex(t, current.State())
	changes := session.NewJournal()
	overlayMove := moved
	overlayMove.ObjectNamespace, overlayMove.ObjectName = d.Namespace(), "#@#"
	added := indexUnitOperator(90002, d.Namespace(), d.Name())
	e.Mu.Lock()
	e.CatalogChangeLocked(changes, moved.Address(), &overlayMove)
	e.CatalogChangeLocked(changes, added.Address(), added)
	inserted := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	assertOperatorIndex(t, inserted.State())
	assertOperatorIndex(t, current.State())
	// Savepoint effects rematerialize from their captured base rather than
	// editing the retained private root's buckets.
	saved := changes.Clone()
	e.Mu.Lock()
	e.CatalogChangeLocked(changes, added.Address(), nil)
	removedAdded := e.PrivateCatalogLocked(changes)
	restored := e.PrivateCatalogLocked(saved)
	e.Mu.Unlock()
	assertOperatorIndex(t, removedAdded.State())
	assertOperatorIndex(t, restored.State())
	assertOperatorIndex(t, inserted.State())
	if restored.Get(added.Address()) != added || removedAdded.Get(added.Address()) != nil {
		t.Fatal("savepoint/private sibling membership changed")
	}
	changes = session.NewJournal()
	e.Mu.Lock()
	e.CatalogChangeLocked(changes, moved.Address(), nil)
	private := e.PrivateCatalogLocked(changes)
	cached := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	if private.State() != cached.State() || private.Get(d.Address()) != nil {
		t.Fatal("private removal/cache did not expose final root")
	}
	assertOperatorIndex(t, private.State())
	assertOperatorIndex(t, current.State())
	changes = session.NewJournal()
	refreshed := moved
	refreshed.ObjectRevision++
	refreshed.Hash = true
	e.Mu.Lock()
	e.CatalogChangeLocked(changes, moved.Address(), &refreshed)
	metadata := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	if metadata.State().OperatorNames() != current.State().OperatorNames() || regobj.CollectOperatorGroups(metadata)[d.Name()][0] != &refreshed {
		t.Fatal("private metadata replacement froze descriptor or copied membership")
	}
	stage = e.BeginCatalogStage(current)
	if err := stage.Remove(e.Catalog.Domain.Ref(current.Epoch(), moved.Address())); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.StageRead(stage).State())
	collationUnitPublish(t, e, stage)
	assertOperatorIndex(t, e.CatalogRead().State())
	assertOperatorIndex(t, published.State())
	assertOperatorIndex(t, current.State())
}

func TestUserOperatorIndexBootstrapAndPrincipalRemap(t *testing.T) {
	source, target := New(), New()
	assertOperatorIndex(t, source.Catalog.State)
	assertOperatorIndex(t, target.Catalog.State)
	if source.Catalog.State.OperatorNames() != target.Catalog.State.OperatorNames() || source.Catalog.State.OperatorNames() != routine.SeededOperatorNames() {
		t.Fatal("seed address index not shared")
	}
	address := ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 96}
	left := source.Catalog.State.MustOperator(address)
	right := target.Catalog.State.MustOperator(address)
	if left == right || left.Owner == right.Owner {
		t.Fatal("engine-owned seed descriptors/principals shared")
	}
	candidate := catalog.CloneState(source.Catalog.State)
	sourceRoles, targetRoles := source.Roles(), target.Roles()
	target.Mu.Lock()
	remapped, _, err := target.remapPreparedCatalog(candidate, sourceRoles, targetRoles, nil)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if remapped.OperatorNames() != source.Catalog.State.OperatorNames() || remapped.Get(address) == left || remapped.MustOperator(address).Owner != right.Owner {
		t.Fatal("principal remap failed fresh provenance/shared membership")
	}
	assertOperatorIndex(t, remapped)
	assertOperatorIndex(t, source.Catalog.State)
}

func TestUserOperatorIndexTemporaryCollisionReplacement(t *testing.T) {
	source, target := New(), New()
	owner := source.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "imported_operators", ObjectRevision: 1, Owner: owner}
	operator := indexUnitOperator(90001, namespace.Address(), "#~#")
	operator.Owner = owner
	stage := source.BeginCatalogStage(source.CatalogRead())
	for _, descriptor := range []catalog.Descriptor{namespace, operator} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic descriptor-only fixture: TEMP operators have no SQL creation
	// route, but replacement's whole-catalog projection must remain exact.
	source.Catalog.PublishLocked(source.StageRead(stage).State())
	tempNamespace := &catalog.Namespace{ObjectAddress: namespace.Address(), ObjectName: "pg_temp_operator_index", ObjectRevision: 1, Owner: target.RoleStore.Domain.Principal(10), Temporary: target.Catalog.Domain.Session(99)}
	temp := indexUnitOperator(operator.Address().ObjectID, tempNamespace.Address(), "#?#")
	temp.Owner = tempNamespace.Owner
	stage = target.BeginCatalogStage(target.CatalogRead())
	for _, descriptor := range []catalog.Descriptor{tempNamespace, temp} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	target.Catalog.PublishLocked(target.StageRead(stage).State())
	retained := target.Catalog.State
	projection, err := control.PersistentReplacementCatalog(target.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	if projection.Get(temp.Address()) != nil || projection.Get(tempNamespace.Address()) != nil {
		t.Fatal("persistent projection retained TEMP membership")
	}
	assertOperatorIndex(t, projection.State())
	assertOperatorIndex(t, retained)
	projectedSource, err := control.PersistentReplacementCatalog(source.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	if projectedSource.State().OperatorNames() != source.Catalog.State.OperatorNames() {
		t.Fatal("TEMP-free projection copied membership")
	}
	next := catalog.CloneState(source.Catalog.State)
	target.Catalog.InstallOIDFrontierLocked(100000)
	target.Mu.Lock()
	err = EnginePreserveTemporaryLocked(target, context.Background(), next, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, target), &catalog.OIDReservation{Base: target.Catalog.NextOID(), Next: target.Catalog.NextOID()})
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Get(temp.Address()) != temp || next.Get(tempNamespace.Address()) != tempNamespace {
		t.Fatal("replacement changed retained TEMP identities")
	}
	groups := regobj.CollectOperatorGroups(catalog.NewRead(nil, next))
	imported := groups[operator.Name()]
	if len(imported) != 1 || imported[0].Address() == operator.Address() || imported[0].Namespace() == namespace.Address() || next.Get(imported[0].Namespace()).Name() != namespace.Name() {
		t.Fatal("imported operator/namespace collision not remapped", imported)
	}
	for _, state := range []*catalog.State{next, source.Catalog.State, retained, projection.State()} {
		assertOperatorIndex(t, state)
	}
}
