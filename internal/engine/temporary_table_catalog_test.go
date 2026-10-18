package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Package-function candidates use real registry descriptors and ownership; no
// public SQL API or manufactured committed DELETE rows are used here.
func temporaryCatalogCandidate(t *testing.T, action parser.OnCommitAction) (*Engine, *Operation, *catalogChanges, *relationDescriptor) {
	t.Helper()
	e := New()
	session := sessionStart(t, e, nil)
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	c := newCatalogChanges()
	namespace := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, name: "pg_temp_candidate", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), temporary: session.token}
	table := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, namespace: namespace.address, name: "candidate", revision: 1, owner: namespace.owner, onCommit: action, columns: []ColumnDescriptor{{Name: "v", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}}, index: map[string]int{"v": 0}, attributes: []int32{1}, attributeHighWater: 1}
	e.mu.Lock()
	e.catalogChangeLocked(c, namespace.address, namespace)
	e.catalogChangeLocked(c, table.address, table)
	c.storage[table.address] = catalogStorageChange{replacement: &relationStorage{}, relation: table}
	e.mu.Unlock()
	return e, op, c, table
}

func TestCatalogTemporaryStorageAbsence(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	if err := e.catalogPreflightLocked(c); err != nil {
		t.Fatal(err)
	}
	if c.storage[table.address].original != nil || e.storage[table.address] != nil {
		t.Fatal("invented committed predecessor")
	}
	mark := cloneCatalogChanges(c)
	replacement := *table
	replacement.onCommit = parser.OnCommitDrop
	e.catalogChangeLocked(c, table.address, &replacement)
	if mark.descriptors[table.address] != table || mark.storage[table.address].replacement != c.storage[table.address].replacement {
		t.Fatal("savepoint descriptor/action/store rewritten")
	}
	e.storage[table.address] = &relationStorage{}
	if _, ok := e.catalogPreflightLocked(c).(CatalogConflict); !ok {
		t.Fatal("committed absence was not fenced")
	}
}

func TestTemporaryTableProviderAction(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitDelete)
	view := e.privateCatalogLocked(c)
	if err := (relationProvider{}).Validate(view, table); err != nil {
		t.Fatal(err)
	}
	invalid := *table
	invalid.namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	c2 := cloneCatalogChanges(c)
	e.catalogChangeLocked(c2, table.address, &invalid)
	if err := (relationProvider{}).Validate(e.privateCatalogLocked(c2), &invalid); err == nil {
		t.Fatal("persistent action descriptor accepted")
	}
	invalid = *table
	invalid.onCommit = parser.OnCommitAction(255)
	if err := (relationProvider{}).Validate(view, &invalid); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestTemporaryTableCommitCandidate(t *testing.T) {
	for _, action := range []parser.OnCommitAction{parser.OnCommitPreserve, parser.OnCommitDelete, parser.OnCommitDrop} {
		e, op, c, table := temporaryCatalogCandidate(t, action)
		writes := map[ids.ObjectAddress][]rowVersion{table.address: {{row: 1, version: 1, values: []sqltype.Value{sqltype.Null(table.columns[0].Usage.Type)}}}}
		mark := cloneCatalogChanges(c)
		nextWrites, _, next, err := e.prepareTemporaryCommit(op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.catalog.state.epoch, nil, 0), writes, nil, c)
		if err != nil {
			t.Fatal(action, err)
		}
		if e.catalog.state.descriptors[table.address] != nil || e.storage[table.address] != nil || len(writes[table.address]) != 1 || c.descriptors[table.address] != table || mark.descriptors[table.address] != table {
			t.Fatal("commit preparation mutated reachable predecessor")
		}
		switch action {
		case parser.OnCommitPreserve:
			if next != c || len(nextWrites[table.address]) != 1 {
				t.Fatal("preserve changed roots")
			}
		case parser.OnCommitDelete:
			if next == c || len(nextWrites[table.address]) != 0 || next.storage[table.address].replacement == c.storage[table.address].replacement || next.descriptors[table.address] != table {
				t.Fatal("delete did not prepare isolated empty store")
			}
		case parser.OnCommitDrop:
			if next == c || len(nextWrites[table.address]) != 0 || next.descriptors[table.address] != nil {
				t.Fatal("drop did not remove private descriptor/writes")
			}
		}
	}
}

func TestTemporaryRelationRetainedProvenance(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	view := e.privateCatalogLocked(c)
	old := e.catalog.domain.Ref(view.Epoch(), table.address)
	advanced := cloneCatalog(view.state)
	advanced.epoch++
	retained := CatalogRead{e, advanced}
	if !storedReferenceEpochValid(retained, table, old) {
		t.Fatal("exact retained owner rejected")
	}
	bad := old
	bad = New().catalog.domain.Ref(bad.Epoch, bad.Address)
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("foreign engine accepted")
	}
	bad = old
	bad.Address.SubID = 2
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("removed attribute accepted")
	}
	bad = old
	bad.Epoch = advanced.epoch + 1
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("future incarnation accepted")
	}
	foreign := *advanced.descriptors[table.namespace].(*namespaceDescriptor)
	foreign.temporary = e.catalog.domain.Session(op.session.token.ID() + 1)
	consumer := *table
	consumer.namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20002}
	advanced.descriptors[consumer.namespace] = &namespaceDescriptor{address: consumer.namespace, temporary: op.session.token}
	advanced.descriptors[table.namespace] = &foreign
	if storedReferenceEpochValid(retained, &consumer, old) {
		t.Fatal("peer provenance accepted")
	}
	advanced.descriptors[table.namespace] = view.state.descriptors[table.namespace]
	delete(advanced.descriptors, table.address)
	if storedReferenceEpochValid(retained, table, old) {
		t.Fatal("retired target resurrected")
	}
}

func TestTemporaryTableCanceledPublication(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitDrop)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := &parser.CreateTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: table.name}}}, Persistence: "temporary", OnCommit: parser.OnCommitDrop}
	if err := scope.gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.ctx = ctx
	original := e.catalog.state
	writes := map[ids.ObjectAddress][]rowVersion{table.address: {{row: 1, version: 1, values: []sqltype.Value{sqltype.Null(table.columns[0].Usage.Type)}}}}
	err = e.publishRows(op, scope.txRef, writes, nil, c, nil, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.catalog.state != original || e.storage[table.address] != nil || c.descriptors[table.address] != table || len(writes[table.address]) != 1 {
		t.Fatal("canceled final commit changed catalog/action/write roots", err)
	}
}

func TestTemporaryTableStagedNamespaceAuthority(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = e.privateCatalogLocked(c)
	auth.recaptureCatalog(auth.catalog)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_temp"}, {Text: "explicit_identity"}}}
	if _, err := e.creationNamespace(auth, name); err == nil {
		t.Fatal("generic creation gained TEMP alias authority")
	}
	auth.stagedTemporary = table.namespace
	ref, err := e.creationNamespace(auth, name)
	if err != nil || ref.Address != table.namespace || temporaryNamespace(auth) != table.namespace {
		t.Fatal("owned stage namespace unresolved", err)
	}
	if op.session.temporaryNamespace != (ids.ObjectAddress{}) || op.session.transactions.catalog != nil {
		t.Fatal("stage namespace published before CREATE success")
	}
	auth.stagedTemporary = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := e.creationNamespace(auth, name); err == nil || temporaryNamespace(auth) != (ids.ObjectAddress{}) {
		t.Fatal("persistent namespace impersonated owner TEMP")
	}
}

func TestTemporaryTableRetainedRegclassManifest(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	options, _, err := bindSequenceOptions(nil, sequenceGenerator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options.persistence = "temporary"
	sequence := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}, namespace: table.namespace, name: "retained_sequence", revision: 1, sequencePlanStamp: 1, owner: table.owner, columns: e.sequenceColumnsLocked(), index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, attributes: []int32{1, 2, 3}, attributeHighWater: 3, relationKind: sequenceRelation, sequence: &options}
	e.mu.Lock()
	e.catalogChangeLocked(c, sequence.address, sequence)
	view := e.privateCatalogLocked(c)
	e.mu.Unlock()
	regclass, ok := view.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("missing builtin regclass")
	}
	ref := e.catalog.domain.Ref(view.Epoch(), sequence.address)
	typeRef := e.catalog.domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)})
	literal := literalExpression{expressionInfo: expressionInfo{Usage: regclass.Usage(), RegclassRef: ref}, value: sqltype.String(regclass, strconv.FormatUint(uint64(sequence.address.ObjectID), 10))}
	next := cloneCatalog(view.state)
	next.epoch++
	retained := CatalogRead{owner: e, state: next}
	var manifest []ids.ObjectRef
	renderer := scalarRenderer{owner: table, dependencies: &manifest, capturedManifest: []ids.ObjectRef{typeRef, ref}}
	if text, err := renderer.sql(context.Background(), retained, literal); err != nil || text == "" {
		t.Fatal("retained literal rejected", err)
	}
	if len(manifest) != 2 || manifest[0] != typeRef || manifest[1] != ref {
		t.Fatal("retained builtin/provider manifest rewritten", manifest)
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), retained, literal); err == nil {
		t.Fatal("generic renderer forgave stale regclass")
	}
	persistent := *table
	persistent.namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := (scalarRenderer{owner: &persistent}).sql(context.Background(), retained, literal); err == nil {
		t.Fatal("persistent consumer gained stale TEMP authority")
	}
	future := literal
	future.RegclassRef.Epoch = retained.Epoch() + 1
	if _, err := renderer.sql(context.Background(), retained, future); err == nil {
		t.Fatal("future regclass epoch admitted")
	}
	foreign := literal
	foreign.RegclassRef = New().catalog.domain.Ref(foreign.RegclassRef.Epoch, foreign.RegclassRef.Address)
	if _, err := renderer.sql(context.Background(), retained, foreign); err == nil {
		t.Fatal("foreign-engine regclass admitted")
	}
	retired := cloneCatalog(next)
	delete(retired.descriptors, sequence.address)
	delete(retired.names, descriptorNameKey(sequence))
	if _, err := renderer.sql(context.Background(), CatalogRead{owner: e, state: retired}, literal); err == nil {
		t.Fatal("retired regclass target resurrected")
	}
}

func TestTemporaryTableRetainedLiveRelationProviders(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE live_provider(v INT)",
		"CREATE INDEX live_provider_index ON live_provider(v)",
		"CREATE TEMP TABLE live_consumer(a INT,g BOOL GENERATED ALWAYS AS('live_provider'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS('live_provider_index'::regclass IS NOT NULL) STORED,z INT,k INT GENERATED ALWAYS AS(z+1) STORED)",
		"INSERT INTO live_consumer(a,z) VALUES(7,9)",
		"CREATE TABLE public.persistent_consumer(v INT)",
	} {
		sessionSQL(t, owner, q)
	}
	sessionSQL(t, peer, "CREATE TEMP TABLE live_provider(v INT)")
	lookup := func(view CatalogRead, name string, namespace ids.SessionToken) *relationDescriptor {
		t.Helper()
		for _, descriptor := range view.state.descriptors {
			d, ok := descriptor.(*relationDescriptor)
			if !ok || d.name != name {
				continue
			}
			ns := view.state.descriptors[d.namespace].(*namespaceDescriptor)
			if ns.temporary == namespace {
				return d
			}
		}
		t.Fatal("missing actual relation", name)
		return nil
	}
	before := e.CatalogRead()
	provider := lookup(before, "live_provider", owner.token)
	providerIndex := lookup(before, "live_provider_index", owner.token)
	consumer := lookup(before, "live_consumer", owner.token)
	persistent := lookup(before, "persistent_consumer", ids.SessionToken{})
	foreign := lookup(before, "live_provider", peer.token)
	store := e.storage[consumer.address]
	indexRoot := e.indexRoots[providerIndex.address]
	if store == nil || len(store.versions) != 1 || indexRoot == nil {
		t.Fatal("missing actual stored row or installed index")
	}
	rows, ends, generation := store.versions, store.ends, store.generation
	dependencies := make(map[int32][]ids.ObjectRef, len(consumer.generations))
	for attribute, definition := range consumer.generations {
		dependencies[attribute] = slices.Clone(definition.dependencies)
	}
	if _, err := e.ReplaceCatalog(context.Background(), New().CatalogRead()); err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	if retained.Epoch() <= before.Epoch() || retained.state.descriptors[provider.address] != provider || retained.state.descriptors[providerIndex.address] != providerIndex || retained.state.descriptors[consumer.address] != consumer || e.storage[consumer.address] != store || e.indexRoots[providerIndex.address] != indexRoot {
		t.Fatal("replacement did not retain actual descriptors/store/root")
	}
	if !sameVersionRoot(store.versions, rows) || store.ends != ends || store.generation != generation {
		t.Fatal("replacement changed immutable row predecessors")
	}
	if err := validateGenerationProviders(retained, consumer); err != nil {
		t.Fatal("live table/index generation providers rejected", err)
	}
	for attribute, definition := range consumer.generations {
		var manifest []ids.ObjectRef
		renderer := scalarRenderer{owner: consumer, capturedManifest: definition.dependencies, dependencies: &manifest}
		if text, err := renderer.sql(context.Background(), retained, definition.expression); err != nil || text == "" {
			t.Fatal("retained generation rendering", attribute, text, err)
		}
		for _, ref := range manifest {
			if !slices.Contains(dependencies[attribute], ref) {
				t.Fatal("captured manifest was restamped or rebound", attribute, ref)
			}
		}
		if !slices.Equal(definition.dependencies, dependencies[attribute]) {
			t.Fatal("immutable dependency manifest changed", attribute)
		}
	}
	for _, target := range []*relationDescriptor{provider, providerIndex} {
		ref := e.catalog.domain.Ref(before.Epoch(), target.address)
		if !storedReferenceEpochValid(retained, consumer, ref) {
			t.Fatal("actual same-owner relation rejected", target.name)
		}
		if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("generic object reference gained old-epoch authority", err)
		}
		if temporarySequenceEpochValid(retained, ref, owner.token) {
			t.Fatal("non-sequence gained sequence runtime authority")
		}
		for _, bad := range []ids.ObjectRef{
			New().catalog.domain.Ref(ref.Epoch, ref.Address),
			e.catalog.domain.Ref(retained.Epoch()+1, ref.Address),
			e.catalog.domain.Ref(0, ref.Address),
			e.catalog.domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID, SubID: 99}),
			e.catalog.domain.Ref(ref.Epoch, foreign.address),
		} {
			if storedReferenceEpochValid(retained, consumer, bad) {
				t.Fatal("invalid contextual reference accepted", bad)
			}
		}
		if storedReferenceEpochValid(New().CatalogRead(), consumer, ref) || storedReferenceEpochValid(retained, persistent, ref) || storedReferenceEpochValid(retained, nil, ref) {
			t.Fatal("foreign engine, persistent or generic consumer gained authority")
		}
	}
	sessionSQL(t, owner, "CREATE DOMAIN public.live_domain AS INT")
	domainView := e.CatalogRead()
	domain := domainUnitType(t, domainView, "live_domain")
	domainRef := e.catalog.domain.Ref(domainView.Epoch(), domain.Address())
	array, ok := domainView.LookupArray(domain.typ)
	if !ok {
		t.Fatal("missing actual domain array")
	}
	arrayRef := e.catalog.domain.Ref(domainView.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})
	if _, err := e.ReplaceCatalog(context.Background(), domainView); err != nil {
		t.Fatal(err)
	}
	retained = e.CatalogRead()
	if _, live := retained.state.descriptors[domainRef.Address]; !live {
		t.Fatal("actual replacement did not retain domain address")
	}
	if storedReferenceEpochValid(retained, consumer, domainRef) || storedReferenceEpochValid(retained, consumer, arrayRef) {
		t.Fatal("domain or domain-array gained stale type authority")
	}
	// This is a real captured row origin, not a provider-kind substitute. Exact
	// concrete Usage and stable attribute/slot/frame checks remain mandatory.
	columnNode := consumer.generations[5].expression.(arithmeticExpression).left
	for {
		cast, ok := columnNode.(coercionExpression)
		if !ok {
			break
		}
		columnNode = cast.child
	}
	column := columnNode.(columnExpression)
	int8, ok := retained.LookupType(sqltype.Int8)
	if !ok {
		t.Fatal("missing actual bigint descriptor")
	}
	for _, change := range []func(*columnExpression){
		func(n *columnExpression) { n.Usage = int8.Usage() },
		func(n *columnExpression) { n.origin.Address.SubID = 99 },
		func(n *columnExpression) { n.slot = 0 },
		func(n *columnExpression) { n.frame = 2 },
	} {
		bad := column
		change(&bad)
		if _, err := (scalarRenderer{owner: consumer}).sql(context.Background(), retained, bad); err == nil {
			t.Fatal("invalid concrete row authority admitted", bad)
		}
	}
	captured := e.catalog.domain.Ref(before.Epoch(), provider.address)
	capturedIndex := e.catalog.domain.Ref(before.Epoch(), providerIndex.address)
	preflight := e.BeginCatalogStage(retained)
	if err := preflight.Remove(e.catalog.domain.Ref(retained.Epoch(), providerIndex.address)); err != nil {
		t.Fatal(err)
	}
	edges, err := domainDependencyClosure(context.Background(), preflight, []ids.ObjectAddress{provider.address})
	if err != nil {
		t.Fatal(err)
	}
	columns := map[int32]int{}
	ownedIndexes := 0
	for _, edge := range edges {
		if edge.address == providerIndex.address {
			if !edge.automatic || edge.relation != providerIndex {
				t.Fatal("lost actual owned-index adjacency", edge)
			}
			ownedIndexes++
		}
		if edge.column != "" && edge.relation.address == consumer.address {
			columns[edge.address.SubID]++
		}
	}
	if ownedIndexes != 1 || columns[2] != 1 || columns[3] != 1 || columns[5] != 0 {
		t.Fatal("pre-removed owned-index closure omitted/duplicated sibling or consumed independent generation", ownedIndexes, columns)
	}
	e.DiscardCatalogStage(preflight)
	if e.CatalogRead().state != retained.state || e.storage[consumer.address] != store || e.indexRoots[providerIndex.address] != indexRoot || !sameVersionRoot(store.versions, rows) || store.ends != ends {
		t.Fatal("closure discovery mutated genuine catalog/row/index predecessors")
	}
	sessionSQL(t, owner, "DROP TABLE live_provider CASCADE")
	if storedReferenceEpochValid(e.CatalogRead(), consumer, captured) || storedReferenceEpochValid(e.CatalogRead(), consumer, capturedIndex) {
		t.Fatal("retired provider resurrected")
	}
	sessionSQL(t, owner, "CREATE TEMP TABLE live_provider(v INT)")
	sessionSQL(t, owner, "CREATE INDEX live_provider_index ON live_provider(v)")
	current := e.CatalogRead()
	if lookup(current, "live_provider", owner.token).address == provider.address || lookup(current, "live_provider_index", owner.token).address == providerIndex.address || storedReferenceEpochValid(current, consumer, captured) || storedReferenceEpochValid(current, consumer, capturedIndex) {
		t.Fatal("same-name recreated provider rebound old identity")
	}
	for attribute, manifest := range dependencies {
		if !slices.Equal(consumer.generations[attribute].dependencies, manifest) {
			t.Fatal("cascade/recreation mutated retained original manifest", attribute)
		}
	}
}
