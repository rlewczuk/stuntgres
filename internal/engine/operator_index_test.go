package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
)

// Independently project authoritative descriptors, then check both directions:
// exact keys and members, no stale/nonoperator addresses and no duplicates.
func assertOperatorIndex(t *testing.T, state *catalogState) {
	t.Helper()
	want := make(map[operatorNameKey]map[ObjectAddress]bool)
	for address, descriptor := range state.descriptors {
		if operator, ok := descriptor.(*operatorDescriptor); ok {
			key := operatorNameKey{operator.namespace, operator.name}
			if want[key] == nil {
				want[key] = make(map[ObjectAddress]bool)
			}
			want[key][address] = true
		}
	}
	if state.operatorNames == nil {
		t.Fatal("catalog root has no operator membership index")
	}
	if len(state.operatorNames.buckets) != len(want) {
		t.Fatalf("bucket keys: got %d, want %d", len(state.operatorNames.buckets), len(want))
	}
	for key, addresses := range state.operatorNames.buckets {
		members := want[key]
		if len(addresses) == 0 || len(addresses) != len(members) {
			t.Fatalf("bucket %v: got %v, want %v", key, addresses, members)
		}
		seen := make(map[ObjectAddress]bool)
		for _, address := range addresses {
			if !members[address] || seen[address] {
				t.Fatalf("bucket %v contains stale, nonoperator or duplicate address %v", key, address)
			}
			seen[address] = true
		}
	}
	groups := collectOperatorGroups(CatalogRead{state: state})
	seen := make(map[ObjectAddress]bool)
	for name, operators := range groups {
		for _, operator := range operators {
			if operator.name != name || state.descriptors[operator.address] != operator || seen[operator.address] {
				t.Fatal("alias groups do not expose exact current descriptors", operator)
			}
			seen[operator.address] = true
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

func indexUnitOperator(id OID, namespace ObjectAddress, name string) *operatorDescriptor {
	return &operatorDescriptor{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: id}, namespace: namespace, name: name, revision: 1, left: 23, right: 23}
}

func TestUserOperatorIndexCopyOnWrite(t *testing.T) {
	namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90000}
	first := indexUnitOperator(90001, namespace, "#~#")
	other := indexUnitOperator(90002, namespace, "#?#")
	base := &catalogState{descriptors: map[ObjectAddress]Descriptor{first.address: first, other.address: other}, names: map[NameKey]ObjectAddress{descriptorNameKey(first): first.address, descriptorNameKey(other): other.address}}
	base.operatorNames = rebuildOperatorNames(base.descriptors)
	key := operatorNameKey{namespace, first.name}
	// This unpublished fixture deliberately retains capacity and a sentinel
	// outside its length: append must not overwrite a sibling's backing array.
	backing := make([]ObjectAddress, 4)
	backing[0], backing[1] = first.address, other.address
	base.operatorNames.buckets[key] = backing[:1]
	e := &Engine{state: base}
	stage := e.BeginCatalogStage(CatalogRead{owner: e, state: base})
	second := indexUnitOperator(90003, namespace, first.name)
	second.left = 0
	if err := stage.Create(second); err != nil {
		t.Fatal(err)
	}
	child := e.stageRead(stage).state
	sibling := cloneCatalog(base)
	third := indexUnitOperator(90004, namespace, first.name)
	third.left, third.right = 20, 20
	stage = e.BeginCatalogStage(CatalogRead{owner: e, state: sibling})
	if err := stage.Create(third); err != nil {
		t.Fatal(err)
	}
	siblingChild := e.stageRead(stage).state
	if base.operatorNames != sibling.operatorNames || child.operatorNames == base.operatorNames || siblingChild.operatorNames == child.operatorNames {
		t.Fatal("root sharing/detachment is incorrect")
	}
	if backing[1] != other.address {
		t.Fatal("child append overwrote shared spare capacity")
	}
	untouched := operatorNameKey{namespace, other.name}
	if &child.operatorNames.buckets[untouched][0] != &base.operatorNames.buckets[untouched][0] {
		t.Fatal("untouched bucket copied")
	}
	for _, state := range []*catalogState{base, sibling, child, siblingChild} {
		assertOperatorIndex(t, state)
	}
	// Multiple removals/inserts in one bucket must neither duplicate members
	// nor mutate either predecessor, regardless of map iteration order.
	stage = e.BeginCatalogStage(CatalogRead{owner: e, state: child})
	moved := *first
	moved.namespace.ObjectID++
	moved.name, moved.revision = "#@#", 2
	if err := stage.Replace(&moved); err != nil {
		t.Fatal(err)
	}
	fourth := indexUnitOperator(90005, namespace, first.name)
	fourth.right = 20
	if err := stage.Create(fourth); err != nil {
		t.Fatal(err)
	}
	movedRoot := e.stageRead(stage).state
	for _, state := range []*catalogState{base, child, siblingChild, movedRoot} {
		assertOperatorIndex(t, state)
	}
}

func TestUserOperatorIndexCurrentMetadata(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	first := indexUnitOperator(90001, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, "#~#")
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(first); err != nil {
		t.Fatal(err)
	}
	base = e.stageRead(stage)
	oldIndex := base.state.operatorNames
	for _, update := range []func(*operatorDescriptor){
		func(d *operatorDescriptor) { d.revision++ },
		func(d *operatorDescriptor) { d.owner = e.authorizationDomain.Principal(10) },
		func(d *operatorDescriptor) { d.left, d.right = 0, 20 },
		func(d *operatorDescriptor) { d.result, d.function = 20, 463 },
		func(d *operatorDescriptor) { d.hash, d.merge, d.negator = true, true, 90002 },
	} {
		previous := base.state.descriptors[first.address].(*operatorDescriptor)
		next := *previous
		update(&next)
		stage = e.BeginCatalogStage(base)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		current := e.stageRead(stage)
		if current.state.operatorNames != oldIndex {
			t.Fatal("metadata-only replacement copied membership")
		}
		oldGroups, newGroups := collectOperatorGroups(base), collectOperatorGroups(current)
		if oldGroups[first.name][0] != previous || newGroups[first.name][0] != &next || base.state.descriptors[first.address] != previous {
			t.Fatal("shared membership froze metadata or changed predecessor")
		}
		assertOperatorIndex(t, base.state)
		assertOperatorIndex(t, current.state)
		base = current
	}
	// A nonoperator stage must retain the exact membership root too.
	namespace := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90010}, name: "unrelated", revision: 1}
	stage = e.BeginCatalogStage(base)
	if err := stage.Create(namespace); err != nil {
		t.Fatal(err)
	}
	unrelated := e.stageRead(stage).state
	if unrelated.operatorNames != oldIndex {
		t.Fatal("unrelated stage copied membership")
	}
	assertOperatorIndex(t, unrelated)
}

func TestUserOperatorIndexPublicationAndOverlay(t *testing.T) {
	e := New()
	before := e.CatalogRead()
	d := indexUnitOperator(90001, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, "#~#")
	d.owner, d.result, d.function = e.authorizationDomain.Principal(10), 23, 177
	stage := e.BeginCatalogStage(before)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.stageRead(stage).state)
	collationUnitPublish(t, e, stage)
	published := e.CatalogRead()
	assertOperatorIndex(t, published.state)
	assertOperatorIndex(t, before.state)
	// Real publication of a namespace move, not just editor-helper assertions.
	moved := *d
	moved.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	moved.revision++
	stage = e.BeginCatalogStage(published)
	if err := stage.Replace(&moved); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.stageRead(stage).state)
	collationUnitPublish(t, e, stage)
	current := e.CatalogRead()
	assertOperatorIndex(t, current.state)
	changes := newCatalogChanges()
	overlayMove := moved
	overlayMove.namespace, overlayMove.name = d.namespace, "#@#"
	added := indexUnitOperator(90002, d.namespace, d.name)
	e.mu.Lock()
	e.catalogChangeLocked(changes, moved.address, &overlayMove)
	e.catalogChangeLocked(changes, added.address, added)
	inserted := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	assertOperatorIndex(t, inserted.state)
	assertOperatorIndex(t, current.state)
	// Savepoint effects rematerialize from their captured base rather than
	// editing the retained private root's buckets.
	saved := cloneCatalogChanges(changes)
	e.mu.Lock()
	e.catalogChangeLocked(changes, added.address, nil)
	removedAdded := e.privateCatalogLocked(changes)
	restored := e.privateCatalogLocked(saved)
	e.mu.Unlock()
	assertOperatorIndex(t, removedAdded.state)
	assertOperatorIndex(t, restored.state)
	assertOperatorIndex(t, inserted.state)
	if restored.state.descriptors[added.address] != added || removedAdded.state.descriptors[added.address] != nil {
		t.Fatal("savepoint/private sibling membership changed")
	}
	changes = newCatalogChanges()
	e.mu.Lock()
	e.catalogChangeLocked(changes, moved.address, nil)
	private := e.privateCatalogLocked(changes)
	cached := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if private.state != cached.state || private.state.descriptors[d.address] != nil {
		t.Fatal("private removal/cache did not expose final root")
	}
	assertOperatorIndex(t, private.state)
	assertOperatorIndex(t, current.state)
	changes = newCatalogChanges()
	refreshed := moved
	refreshed.revision++
	refreshed.hash = true
	e.mu.Lock()
	e.catalogChangeLocked(changes, moved.address, &refreshed)
	metadata := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if metadata.state.operatorNames != current.state.operatorNames || collectOperatorGroups(metadata)[d.name][0] != &refreshed {
		t.Fatal("private metadata replacement froze descriptor or copied membership")
	}
	stage = e.BeginCatalogStage(current)
	if err := stage.Remove(e.domain.Ref(current.Epoch(), moved.address)); err != nil {
		t.Fatal(err)
	}
	assertOperatorIndex(t, e.stageRead(stage).state)
	collationUnitPublish(t, e, stage)
	assertOperatorIndex(t, e.CatalogRead().state)
	assertOperatorIndex(t, published.state)
	assertOperatorIndex(t, current.state)
}

func TestUserOperatorIndexBootstrapAndPrincipalRemap(t *testing.T) {
	source, target := New(), New()
	assertOperatorIndex(t, source.state)
	assertOperatorIndex(t, target.state)
	if source.state.operatorNames != target.state.operatorNames || source.state.operatorNames != seededOperatorNames {
		t.Fatal("seed address index not shared")
	}
	address := ObjectAddress{ClassID: OperatorClassID, ObjectID: 96}
	left := source.state.descriptors[address].(*operatorDescriptor)
	right := target.state.descriptors[address].(*operatorDescriptor)
	if left == right || left.owner == right.owner {
		t.Fatal("engine-owned seed descriptors/principals shared")
	}
	candidate := cloneCatalog(source.state)
	sourceRoles, targetRoles := source.Roles(), target.Roles()
	target.mu.Lock()
	remapped, err := target.remapPreparedCatalog(candidate, sourceRoles, targetRoles, nil)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if remapped.operatorNames != source.state.operatorNames || remapped.descriptors[address] == left || remapped.descriptors[address].(*operatorDescriptor).owner != right.owner {
		t.Fatal("principal remap failed fresh provenance/shared membership")
	}
	assertOperatorIndex(t, remapped)
	assertOperatorIndex(t, source.state)
}

func TestUserOperatorIndexTemporaryCollisionReplacement(t *testing.T) {
	source, target := New(), New()
	owner := source.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	namespace := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 90000}, name: "imported_operators", revision: 1, owner: owner}
	operator := indexUnitOperator(90001, namespace.address, "#~#")
	operator.owner = owner
	stage := source.BeginCatalogStage(source.CatalogRead())
	for _, descriptor := range []Descriptor{namespace, operator} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic descriptor-only fixture: TEMP operators have no SQL creation
	// route, but replacement's whole-catalog projection must remain exact.
	source.state = source.stageRead(stage).state
	tempNamespace := &namespaceDescriptor{address: namespace.address, name: "pg_temp_operator_index", revision: 1, owner: target.authorizationDomain.Principal(10), temporary: target.domain.Session(99)}
	temp := indexUnitOperator(operator.address.ObjectID, tempNamespace.address, "#?#")
	temp.owner = tempNamespace.owner
	stage = target.BeginCatalogStage(target.CatalogRead())
	for _, descriptor := range []Descriptor{tempNamespace, temp} {
		if err := stage.Create(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	target.state = target.stageRead(stage).state
	retained := target.state
	projection, err := persistentReplacementCatalog(target.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	if projection.state.descriptors[temp.address] != nil || projection.state.descriptors[tempNamespace.address] != nil {
		t.Fatal("persistent projection retained TEMP membership")
	}
	assertOperatorIndex(t, projection.state)
	assertOperatorIndex(t, retained)
	projectedSource, err := persistentReplacementCatalog(source.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	if projectedSource.state.operatorNames != source.state.operatorNames {
		t.Fatal("TEMP-free projection copied membership")
	}
	next := cloneCatalog(source.state)
	target.nextOID = 100000
	target.mu.Lock()
	err = target.preserveTemporaryLocked(context.Background(), next, map[ObjectAddress]*relationStorage{}, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.descriptors[temp.address] != temp || next.descriptors[tempNamespace.address] != tempNamespace {
		t.Fatal("replacement changed retained TEMP identities")
	}
	groups := collectOperatorGroups(CatalogRead{state: next})
	imported := groups[operator.name]
	if len(imported) != 1 || imported[0].address == operator.address || imported[0].namespace == namespace.address || next.descriptors[imported[0].namespace].Name() != namespace.name {
		t.Fatal("imported operator/namespace collision not remapped", imported)
	}
	for _, state := range []*catalogState{next, source.state, retained, projection.state} {
		assertOperatorIndex(t, state)
	}
}
