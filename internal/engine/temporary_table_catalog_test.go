package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Package-function candidates use real registry descriptors and ownership; no
// public SQL API or manufactured committed DELETE rows are used here.
func temporaryCatalogCandidate(t *testing.T, action parser.OnCommitAction) (*Engine, *Operation, *catalogChanges, *catalog.Relation) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	c := session.NewJournal()
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, ObjectName: "pg_temp_candidate", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: s.SessionToken}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: namespace.Address(), ObjectName: "candidate", ObjectRevision: 1, Owner: namespace.Owner, OnCommit: action, Columns: []catalog.Column{{Name: "v", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	e.mu.Lock()
	e.catalogChangeLocked(c, namespace.Address(), namespace)
	e.catalogChangeLocked(c, table.Address(), table)
	c.Storage[table.Address()] = catalogStorageChange{Replacement: &storage.Relation{}, Relation: table}
	e.mu.Unlock()
	return e, op, c, table
}

func TestCatalogTemporaryStorageAbsence(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	if err := e.catalogPreflightLocked(c); err != nil {
		t.Fatal(err)
	}
	if c.Storage[table.Address()].Original != nil || e.storage.Relations[table.Address()] != nil {
		t.Fatal("invented committed predecessor")
	}
	mark := c.Clone()
	replacement := *table
	replacement.OnCommit = parser.OnCommitDrop
	e.catalogChangeLocked(c, table.Address(), &replacement)
	if mark.Descriptors[table.Address()] != table || mark.Storage[table.Address()].Replacement != c.Storage[table.Address()].Replacement {
		t.Fatal("savepoint descriptor/action/store rewritten")
	}
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.mu.Unlock()
	if _, ok := e.catalogPreflightLocked(c).(catalog.Conflict); !ok {
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
	invalid.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	c2 := c.Clone()
	e.catalogChangeLocked(c2, table.Address(), &invalid)
	if err := (relationProvider{}).Validate(e.privateCatalogLocked(c2), &invalid); err == nil {
		t.Fatal("persistent action descriptor accepted")
	}
	invalid = *table
	invalid.OnCommit = parser.OnCommitAction(255)
	if err := (relationProvider{}).Validate(view, &invalid); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestTemporaryTableCommitCandidate(t *testing.T) {
	for _, action := range []parser.OnCommitAction{parser.OnCommitPreserve, parser.OnCommitDelete, parser.OnCommitDrop} {
		e, op, c, table := temporaryCatalogCandidate(t, action)
		writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
		mark := c.Clone()
		nextWrites, _, next, err := e.prepareTemporaryCommit(op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.catalog.state.Epoch(), nil, 0), writes, nil, c)
		if err != nil {
			t.Fatal(action, err)
		}
		if e.catalog.state.Get(table.Address()) != nil || e.storage.Relations[table.Address()] != nil || len(writes[table.Address()]) != 1 || c.Descriptors[table.Address()] != table || mark.Descriptors[table.Address()] != table {
			t.Fatal("commit preparation mutated reachable predecessor")
		}
		switch action {
		case parser.OnCommitPreserve:
			if next != c || len(nextWrites[table.Address()]) != 1 {
				t.Fatal("preserve changed roots")
			}
		case parser.OnCommitDelete:
			if next == c || len(nextWrites[table.Address()]) != 0 || next.Storage[table.Address()].Replacement == c.Storage[table.Address()].Replacement || next.Descriptors[table.Address()] != table {
				t.Fatal("delete did not prepare isolated empty store")
			}
		case parser.OnCommitDrop:
			if next == c || len(nextWrites[table.Address()]) != 0 || next.Descriptors[table.Address()] != nil {
				t.Fatal("drop did not remove private descriptor/writes")
			}
		}
	}
}

func TestTemporaryRelationRetainedProvenance(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	view := e.privateCatalogLocked(c)
	old := e.catalog.Domain.Ref(view.Epoch(), table.Address())
	advanced := catalog.CloneState(view.State())
	advanced.SetEpoch(advanced.Epoch() + 1)
	retained := e.catalog.read(advanced)
	if !storedReferenceEpochValid(retained, table, old) {
		t.Fatal("exact retained owner rejected")
	}
	bad := old
	bad = New().catalog.Domain.Ref(bad.Epoch, bad.Address)
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("foreign engine accepted")
	}
	bad = old
	bad.Address.SubID = 2
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("removed attribute accepted")
	}
	bad = old
	bad.Epoch = advanced.Epoch() + 1
	if storedReferenceEpochValid(retained, table, bad) {
		t.Fatal("future incarnation accepted")
	}
	foreign := *advanced.MustNamespace(table.Namespace())
	foreign.Temporary = e.catalog.Domain.Session(op.Session.SessionToken.ID() + 1)
	consumer := *table
	consumer.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20002}
	advanced.SetDescriptor(consumer.Namespace(), &catalog.Namespace{ObjectAddress: consumer.Namespace(), Temporary: op.Session.SessionToken})
	advanced.SetDescriptor(table.Namespace(), &foreign)
	if storedReferenceEpochValid(retained, &consumer, old) {
		t.Fatal("peer provenance accepted")
	}
	advanced.SetDescriptor(table.Namespace(), view.Get(table.Namespace()))
	advanced.RemoveDescriptor(table.Address())
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
	raw := &parser.CreateTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: table.Name()}}}, Persistence: "temporary", OnCommit: parser.OnCommitDrop}
	if err := scope.gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	original := e.catalog.state
	writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
	err = e.publishRows(op, scope.txRef, writes, nil, c, nil, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.catalog.state != original || e.storage.Relations[table.Address()] != nil || c.Descriptors[table.Address()] != table || len(writes[table.Address()]) != 1 {
		t.Fatal("canceled final commit changed catalog/action/write roots", err)
	}
}

func TestTemporaryTableStagedNamespaceAuthority(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = e.privateCatalogLocked(c)
	auth.Recapture(auth.Catalog)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_temp"}, {Text: "explicit_identity"}}}
	if _, err := e.creationNamespace(auth, name); err == nil {
		t.Fatal("generic creation gained TEMP alias authority")
	}
	auth.stagedTemporary = table.Namespace()
	ref, err := e.creationNamespace(auth, name)
	if err != nil || ref.Address != table.Namespace() || temporaryNamespace(auth) != table.Namespace() {
		t.Fatal("owned stage namespace unresolved", err)
	}
	if op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || op.Session.Transactions.catalog != nil {
		t.Fatal("stage namespace published before CREATE success")
	}
	auth.stagedTemporary = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := e.creationNamespace(auth, name); err == nil || temporaryNamespace(auth) != (ids.ObjectAddress{}) {
		t.Fatal("persistent namespace impersonated owner TEMP")
	}
}

func TestTemporaryTableRetainedRegclassManifest(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options.Persistence = "temporary"
	sequence := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}, ObjectNamespace: table.Namespace(), ObjectName: "retained_sequence", ObjectRevision: 1, SequencePlanStamp: 1, Owner: table.Owner, Columns: e.sequenceColumnsLocked(), Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
	e.mu.Lock()
	e.catalogChangeLocked(c, sequence.Address(), sequence)
	view := e.privateCatalogLocked(c)
	e.mu.Unlock()
	regclass, ok := view.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("missing builtin regclass")
	}
	ref := e.catalog.Domain.Ref(view.Epoch(), sequence.Address())
	typeRef := e.catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)})
	literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref}, Value: sqltype.String(regclass, strconv.FormatUint(uint64(sequence.Address().ObjectID), 10))}
	next := catalog.CloneState(view.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.catalog.read(next)
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
	persistent.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := (scalarRenderer{owner: &persistent}).sql(context.Background(), retained, literal); err == nil {
		t.Fatal("persistent consumer gained stale TEMP authority")
	}
	future := literal
	future.RegclassRef.Epoch = retained.Epoch() + 1
	if _, err := renderer.sql(context.Background(), retained, future); err == nil {
		t.Fatal("future regclass epoch admitted")
	}
	foreign := literal
	foreign.RegclassRef = New().catalog.Domain.Ref(foreign.RegclassRef.Epoch, foreign.RegclassRef.Address)
	if _, err := renderer.sql(context.Background(), retained, foreign); err == nil {
		t.Fatal("foreign-engine regclass admitted")
	}
	retired := catalog.CloneState(next)
	retired.RemoveDescriptor(sequence.Address())
	retired.UnbindName(catalog.DescriptorNameKey(sequence))
	if _, err := renderer.sql(context.Background(), e.catalog.read(retired), literal); err == nil {
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
	lookup := func(view catalog.Read, name string, namespace ids.SessionToken) *catalog.Relation {
		t.Helper()
		for _, descriptor := range view.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || d.Name() != name {
				continue
			}
			ns := view.MustNamespace(d.Namespace())
			if ns.Temporary == namespace {
				return d
			}
		}
		t.Fatal("missing actual relation", name)
		return nil
	}
	before := e.CatalogRead()
	provider := lookup(before, "live_provider", owner.SessionToken)
	providerIndex := lookup(before, "live_provider_index", owner.SessionToken)
	consumer := lookup(before, "live_consumer", owner.SessionToken)
	persistent := lookup(before, "persistent_consumer", ids.SessionToken{})
	foreign := lookup(before, "live_provider", peer.SessionToken)
	store := e.storage.Relations[consumer.Address()]
	indexRoot := e.storage.IndexRoots[providerIndex.Address()]
	if store == nil || len(store.Versions) != 1 || indexRoot == nil {
		t.Fatal("missing actual stored row or installed index")
	}
	rows, ends, generation := store.Versions, store.Ends, store.Generation
	dependencies := make(map[int32][]ids.ObjectRef, len(consumer.ColumnMeta.Generations))
	for attribute, definition := range consumer.ColumnMeta.Generations {
		dependencies[attribute] = slices.Clone(definition.Dependencies)
	}
	if _, err := replaceWithFreshCatalog(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	if retained.Epoch() <= before.Epoch() || retained.Get(provider.Address()) != provider || retained.Get(providerIndex.Address()) != providerIndex || retained.Get(consumer.Address()) != consumer || e.storage.Relations[consumer.Address()] != store || e.storage.IndexRoots[providerIndex.Address()] != indexRoot {
		t.Fatal("replacement did not retain actual descriptors/store/root")
	}
	if !sameVersionRoot(store.Versions, rows) || store.Ends != ends || store.Generation != generation {
		t.Fatal("replacement changed immutable row predecessors")
	}
	if err := validateGenerationProviders(retained, consumer); err != nil {
		t.Fatal("live table/index generation providers rejected", err)
	}
	for attribute, definition := range consumer.ColumnMeta.Generations {
		var manifest []ids.ObjectRef
		renderer := scalarRenderer{owner: consumer, capturedManifest: definition.Dependencies, dependencies: &manifest}
		if text, err := renderer.sql(context.Background(), retained, definition.Expression); err != nil || text == "" {
			t.Fatal("retained generation rendering", attribute, text, err)
		}
		for _, ref := range manifest {
			if !slices.Contains(dependencies[attribute], ref) {
				t.Fatal("captured manifest was restamped or rebound", attribute, ref)
			}
		}
		if !slices.Equal(definition.Dependencies, dependencies[attribute]) {
			t.Fatal("immutable dependency manifest changed", attribute)
		}
	}
	for _, target := range []*catalog.Relation{provider, providerIndex} {
		ref := e.catalog.Domain.Ref(before.Epoch(), target.Address())
		if !storedReferenceEpochValid(retained, consumer, ref) {
			t.Fatal("actual same-owner relation rejected", target.Name())
		}
		if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("generic object reference gained old-epoch authority", err)
		}
		if temporarySequenceEpochValid(retained, ref, owner.SessionToken) {
			t.Fatal("non-sequence gained sequence runtime authority")
		}
		for _, bad := range []ids.ObjectRef{
			New().catalog.Domain.Ref(ref.Epoch, ref.Address),
			e.catalog.Domain.Ref(retained.Epoch()+1, ref.Address),
			e.catalog.Domain.Ref(0, ref.Address),
			e.catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID, SubID: 99}),
			e.catalog.Domain.Ref(ref.Epoch, foreign.Address()),
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
	domainRef := e.catalog.Domain.Ref(domainView.Epoch(), domain.Address())
	array, ok := domainView.LookupArray(domain.SQLType)
	if !ok {
		t.Fatal("missing actual domain array")
	}
	arrayRef := e.catalog.Domain.Ref(domainView.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})
	if _, err := e.ReplaceCatalog(context.Background(), e, domainView); err != nil {
		t.Fatal(err)
	}
	retained = e.CatalogRead()
	if _, live := retained.Find(domainRef.Address); !live {
		t.Fatal("actual replacement did not retain domain address")
	}
	if storedReferenceEpochValid(retained, consumer, domainRef) || storedReferenceEpochValid(retained, consumer, arrayRef) {
		t.Fatal("domain or domain-array gained stale type authority")
	}
	// This is a real captured row origin, not a provider-kind substitute. Exact
	// concrete Usage and stable attribute/slot/frame checks remain mandatory.
	columnNode := consumer.ColumnMeta.Generations[5].Expression.(plan.ArithmeticExpr).Left
	for {
		cast, ok := columnNode.(plan.CoercionExpr)
		if !ok {
			break
		}
		columnNode = cast.Child
	}
	column := columnNode.(plan.ColumnExpr)
	int8, ok := retained.LookupType(sqltype.Int8)
	if !ok {
		t.Fatal("missing actual bigint descriptor")
	}
	for _, change := range []func(*plan.ColumnExpr){
		func(n *plan.ColumnExpr) { n.Usage = int8.Usage() },
		func(n *plan.ColumnExpr) { n.Origin.Address.SubID = 99 },
		func(n *plan.ColumnExpr) { n.Slot = 0 },
		func(n *plan.ColumnExpr) { n.Frame = 2 },
	} {
		bad := column
		change(&bad)
		if _, err := (scalarRenderer{owner: consumer}).sql(context.Background(), retained, bad); err == nil {
			t.Fatal("invalid concrete row authority admitted", bad)
		}
	}
	captured := e.catalog.Domain.Ref(before.Epoch(), provider.Address())
	capturedIndex := e.catalog.Domain.Ref(before.Epoch(), providerIndex.Address())
	preflight := e.BeginCatalogStage(retained)
	if err := preflight.Remove(e.catalog.Domain.Ref(retained.Epoch(), providerIndex.Address())); err != nil {
		t.Fatal(err)
	}
	edges, err := domainDependencyClosure(context.Background(), preflight, []ids.ObjectAddress{provider.Address()})
	if err != nil {
		t.Fatal(err)
	}
	columns := map[int32]int{}
	ownedIndexes := 0
	for _, edge := range edges {
		if edge.address == providerIndex.Address() {
			if !edge.automatic || edge.relation != providerIndex {
				t.Fatal("lost actual owned-index adjacency", edge)
			}
			ownedIndexes++
		}
		if edge.column != "" && edge.relation.Address() == consumer.Address() {
			columns[edge.address.SubID]++
		}
	}
	if ownedIndexes != 1 || columns[2] != 1 || columns[3] != 1 || columns[5] != 0 {
		t.Fatal("pre-removed owned-index closure omitted/duplicated sibling or consumed independent generation", ownedIndexes, columns)
	}
	e.DiscardCatalogStage(preflight)
	if e.CatalogRead().State() != retained.State() || e.storage.Relations[consumer.Address()] != store || e.storage.IndexRoots[providerIndex.Address()] != indexRoot || !sameVersionRoot(store.Versions, rows) || store.Ends != ends {
		t.Fatal("closure discovery mutated genuine catalog/row/index predecessors")
	}
	sessionSQL(t, owner, "DROP TABLE live_provider CASCADE")
	if storedReferenceEpochValid(e.CatalogRead(), consumer, captured) || storedReferenceEpochValid(e.CatalogRead(), consumer, capturedIndex) {
		t.Fatal("retired provider resurrected")
	}
	sessionSQL(t, owner, "CREATE TEMP TABLE live_provider(v INT)")
	sessionSQL(t, owner, "CREATE INDEX live_provider_index ON live_provider(v)")
	current := e.CatalogRead()
	if lookup(current, "live_provider", owner.SessionToken).Address() == provider.Address() || lookup(current, "live_provider_index", owner.SessionToken).Address() == providerIndex.Address() || storedReferenceEpochValid(current, consumer, captured) || storedReferenceEpochValid(current, consumer, capturedIndex) {
		t.Fatal("same-name recreated provider rebound old identity")
	}
	for attribute, manifest := range dependencies {
		if !slices.Equal(consumer.ColumnMeta.Generations[attribute].Dependencies, manifest) {
			t.Fatal("cascade/recreation mutated retained original manifest", attribute)
		}
	}
}
