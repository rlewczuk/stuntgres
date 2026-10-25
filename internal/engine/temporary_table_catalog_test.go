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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Package-function candidates use real registry descriptors and ownership; no
// public SQL API or manufactured committed DELETE rows are used here.
func temporaryCatalogCandidate(t *testing.T, action parser.OnCommitAction) (*Engine, *session.Operation, *session.Journal, *catalog.Relation) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	c := session.NewJournal()
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, ObjectName: "pg_temp_candidate", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: s.SessionToken}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: namespace.Address(), ObjectName: "candidate", ObjectRevision: 1, Owner: namespace.Owner, OnCommit: action, Columns: []catalog.Column{{Name: "v", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	e.Mu.Lock()
	e.CatalogChangeLocked(c, namespace.Address(), namespace)
	e.CatalogChangeLocked(c, table.Address(), table)
	c.Storage[table.Address()] = session.StorageChange{Replacement: &storage.Relation{}, Relation: table}
	e.Mu.Unlock()
	return e, op, c, table
}

func TestCatalogTemporaryStorageAbsence(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	if err := e.CatalogPreflightLocked(c); err != nil {
		t.Fatal(err)
	}
	if c.Storage[table.Address()].Original != nil || e.Storage.Relations[table.Address()] != nil {
		t.Fatal("invented committed predecessor")
	}
	mark := c.Clone()
	replacement := *table
	replacement.OnCommit = parser.OnCommitDrop
	e.CatalogChangeLocked(c, table.Address(), &replacement)
	if mark.Descriptors[table.Address()] != table || mark.Storage[table.Address()].Replacement != c.Storage[table.Address()].Replacement {
		t.Fatal("savepoint descriptor/action/store rewritten")
	}
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.Mu.Unlock()
	if _, ok := e.CatalogPreflightLocked(c).(catalog.Conflict); !ok {
		t.Fatal("committed absence was not fenced")
	}
}

func TestTemporaryTableProviderAction(t *testing.T) {
	e, _, c, table := temporaryCatalogCandidate(t, parser.OnCommitDelete)
	view := e.PrivateCatalogLocked(c)
	if err := (RelationProvider()).Validate(view, table); err != nil {
		t.Fatal(err)
	}
	invalid := *table
	invalid.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	c2 := c.Clone()
	e.CatalogChangeLocked(c2, table.Address(), &invalid)
	if err := (RelationProvider()).Validate(e.PrivateCatalogLocked(c2), &invalid); err == nil {
		t.Fatal("persistent action descriptor accepted")
	}
	invalid = *table
	invalid.OnCommit = parser.OnCommitAction(255)
	if err := (RelationProvider()).Validate(view, &invalid); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestTemporaryTableCommitCandidate(t *testing.T) {
	for _, action := range []parser.OnCommitAction{parser.OnCommitPreserve, parser.OnCommitDelete, parser.OnCommitDrop} {
		e, op, c, table := temporaryCatalogCandidate(t, action)
		writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
		mark := c.Clone()
		nextWrites, _, next, err := EnginePrepareTemporaryCommit(e, op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.Catalog.State.Epoch(), nil, 0), writes, nil, c)
		if err != nil {
			t.Fatal(action, err)
		}
		if e.Catalog.State.Get(table.Address()) != nil || e.Storage.Relations[table.Address()] != nil || len(writes[table.Address()]) != 1 || c.Descriptors[table.Address()] != table || mark.Descriptors[table.Address()] != table {
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
	view := e.PrivateCatalogLocked(c)
	old := e.Catalog.Domain.Ref(view.Epoch(), table.Address())
	advanced := catalog.CloneState(view.State())
	advanced.SetEpoch(advanced.Epoch() + 1)
	retained := e.Catalog.Read(advanced)
	if !stored.ReferenceEpochValid(retained, table, old) {
		t.Fatal("exact retained owner rejected")
	}
	bad := old
	bad = New().Catalog.Domain.Ref(bad.Epoch, bad.Address)
	if stored.ReferenceEpochValid(retained, table, bad) {
		t.Fatal("foreign engine accepted")
	}
	bad = old
	bad.Address.SubID = 2
	if stored.ReferenceEpochValid(retained, table, bad) {
		t.Fatal("removed attribute accepted")
	}
	bad = old
	bad.Epoch = advanced.Epoch() + 1
	if stored.ReferenceEpochValid(retained, table, bad) {
		t.Fatal("future incarnation accepted")
	}
	foreign := *advanced.MustNamespace(table.Namespace())
	foreign.Temporary = e.Catalog.Domain.Session(op.Session.SessionToken.ID() + 1)
	consumer := *table
	consumer.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20002}
	advanced.SetDescriptor(consumer.Namespace(), &catalog.Namespace{ObjectAddress: consumer.Namespace(), Temporary: op.Session.SessionToken})
	advanced.SetDescriptor(table.Namespace(), &foreign)
	if stored.ReferenceEpochValid(retained, &consumer, old) {
		t.Fatal("peer provenance accepted")
	}
	advanced.SetDescriptor(table.Namespace(), view.Get(table.Namespace()))
	advanced.RemoveDescriptor(table.Address())
	if stored.ReferenceEpochValid(retained, table, old) {
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
	if err := scope.Gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	original := e.Catalog.State
	writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
	err = EnginePublishRows(e, op, scope.TxRef, writes, nil, c, nil, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.Catalog.State != original || e.Storage.Relations[table.Address()] != nil || c.Descriptors[table.Address()] != table || len(writes[table.Address()]) != 1 {
		t.Fatal("canceled final commit changed catalog/action/write roots", err)
	}
}

func TestTemporaryTableStagedNamespaceAuthority(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = e.PrivateCatalogLocked(c)
	auth.Recapture(auth.Catalog)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_temp"}, {Text: "explicit_identity"}}}
	if _, err := e.CreationNamespace(auth, name); err == nil {
		t.Fatal("generic creation gained TEMP alias authority")
	}
	auth.StagedTemporary = table.Namespace()
	ref, err := e.CreationNamespace(auth, name)
	if err != nil || ref.Address != table.Namespace() || TemporaryNamespace(auth) != table.Namespace() {
		t.Fatal("owned stage namespace unresolved", err)
	}
	if op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || op.Session.Transactions.Catalog != nil {
		t.Fatal("stage namespace published before CREATE success")
	}
	auth.StagedTemporary = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := e.CreationNamespace(auth, name); err == nil || TemporaryNamespace(auth) != (ids.ObjectAddress{}) {
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
	columns := sequence.Columns(e.Catalog.Current())
	sequence := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}, ObjectNamespace: table.Namespace(), ObjectName: "retained_sequence", ObjectRevision: 1, SequencePlanStamp: 1, Owner: table.Owner, Columns: columns, Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
	e.Mu.Lock()
	e.CatalogChangeLocked(c, sequence.Address(), sequence)
	view := e.PrivateCatalogLocked(c)
	e.Mu.Unlock()
	regclass, ok := view.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("missing builtin regclass")
	}
	ref := e.Catalog.Domain.Ref(view.Epoch(), sequence.Address())
	typeRef := e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)})
	literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref}, Value: sqltype.String(regclass, strconv.FormatUint(uint64(sequence.Address().ObjectID), 10))}
	next := catalog.CloneState(view.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.Catalog.Read(next)
	var manifest []ids.ObjectRef
	renderer := stored.ScalarRenderer{Owner: table, Dependencies: &manifest, CapturedManifest: []ids.ObjectRef{typeRef, ref}}
	if text, err := renderer.SQL(context.Background(), retained, literal); err != nil || text == "" {
		t.Fatal("retained literal rejected", err)
	}
	if len(manifest) != 2 || manifest[0] != typeRef || manifest[1] != ref {
		t.Fatal("retained builtin/provider manifest rewritten", manifest)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), retained, literal); err == nil {
		t.Fatal("generic renderer forgave stale regclass")
	}
	persistent := *table
	persistent.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if _, err := (stored.ScalarRenderer{Owner: &persistent}).SQL(context.Background(), retained, literal); err == nil {
		t.Fatal("persistent consumer gained stale TEMP authority")
	}
	future := literal
	future.RegclassRef.Epoch = retained.Epoch() + 1
	if _, err := renderer.SQL(context.Background(), retained, future); err == nil {
		t.Fatal("future regclass epoch admitted")
	}
	foreign := literal
	foreign.RegclassRef = New().Catalog.Domain.Ref(foreign.RegclassRef.Epoch, foreign.RegclassRef.Address)
	if _, err := renderer.SQL(context.Background(), retained, foreign); err == nil {
		t.Fatal("foreign-engine regclass admitted")
	}
	retired := catalog.CloneState(next)
	retired.RemoveDescriptor(sequence.Address())
	retired.UnbindName(catalog.DescriptorNameKey(sequence))
	if _, err := renderer.SQL(context.Background(), e.Catalog.Read(retired), literal); err == nil {
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
	store := e.Storage.Relations[consumer.Address()]
	indexRoot := e.Storage.IndexRoots[providerIndex.Address()]
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
	if retained.Epoch() <= before.Epoch() || retained.Get(provider.Address()) != provider || retained.Get(providerIndex.Address()) != providerIndex || retained.Get(consumer.Address()) != consumer || e.Storage.Relations[consumer.Address()] != store || e.Storage.IndexRoots[providerIndex.Address()] != indexRoot {
		t.Fatal("replacement did not retain actual descriptors/store/root")
	}
	if !storage.SameVersionRoot(store.Versions, rows) || store.Ends != ends || store.Generation != generation {
		t.Fatal("replacement changed immutable row predecessors")
	}
	if err := table.ValidateGenerationProviders(retained, consumer); err != nil {
		t.Fatal("live table/index generation providers rejected", err)
	}
	for attribute, definition := range consumer.ColumnMeta.Generations {
		var manifest []ids.ObjectRef
		renderer := stored.ScalarRenderer{Owner: consumer, CapturedManifest: definition.Dependencies, Dependencies: &manifest}
		if text, err := renderer.SQL(context.Background(), retained, definition.Expression); err != nil || text == "" {
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
		ref := e.Catalog.Domain.Ref(before.Epoch(), target.Address())
		if !stored.ReferenceEpochValid(retained, consumer, ref) {
			t.Fatal("actual same-owner relation rejected", target.Name())
		}
		if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("generic object reference gained old-epoch authority", err)
		}
		if sequence.TemporaryEpochValid(retained, ref, owner.SessionToken) {
			t.Fatal("non-sequence gained sequence runtime authority")
		}
		for _, bad := range []ids.ObjectRef{
			New().Catalog.Domain.Ref(ref.Epoch, ref.Address),
			e.Catalog.Domain.Ref(retained.Epoch()+1, ref.Address),
			e.Catalog.Domain.Ref(0, ref.Address),
			e.Catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID, SubID: 99}),
			e.Catalog.Domain.Ref(ref.Epoch, foreign.Address()),
		} {
			if stored.ReferenceEpochValid(retained, consumer, bad) {
				t.Fatal("invalid contextual reference accepted", bad)
			}
		}
		if stored.ReferenceEpochValid(New().CatalogRead(), consumer, ref) || stored.ReferenceEpochValid(retained, persistent, ref) || stored.ReferenceEpochValid(retained, nil, ref) {
			t.Fatal("foreign engine, persistent or generic consumer gained authority")
		}
	}
	sessionSQL(t, owner, "CREATE DOMAIN public.live_domain AS INT")
	domainView := e.CatalogRead()
	domain := domainUnitType(t, domainView, "live_domain")
	domainRef := e.Catalog.Domain.Ref(domainView.Epoch(), domain.Address())
	array, ok := domainView.LookupArray(domain.SQLType)
	if !ok {
		t.Fatal("missing actual domain array")
	}
	arrayRef := e.Catalog.Domain.Ref(domainView.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})
	if _, err := e.ReplaceCatalog(context.Background(), e, domainView); err != nil {
		t.Fatal(err)
	}
	retained = e.CatalogRead()
	if _, live := retained.Find(domainRef.Address); !live {
		t.Fatal("actual replacement did not retain domain address")
	}
	if stored.ReferenceEpochValid(retained, consumer, domainRef) || stored.ReferenceEpochValid(retained, consumer, arrayRef) {
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
		if _, err := (stored.ScalarRenderer{Owner: consumer}).SQL(context.Background(), retained, bad); err == nil {
			t.Fatal("invalid concrete row authority admitted", bad)
		}
	}
	captured := e.Catalog.Domain.Ref(before.Epoch(), provider.Address())
	capturedIndex := e.Catalog.Domain.Ref(before.Epoch(), providerIndex.Address())
	preflight := e.BeginCatalogStage(retained)
	if err := preflight.Remove(e.Catalog.Domain.Ref(retained.Epoch(), providerIndex.Address())); err != nil {
		t.Fatal(err)
	}
	edges, err := DomainDependencyClosure(context.Background(), preflight, []ids.ObjectAddress{provider.Address()})
	if err != nil {
		t.Fatal(err)
	}
	columns := map[int32]int{}
	ownedIndexes := 0
	for _, edge := range edges {
		if edge.Address == providerIndex.Address() {
			if !edge.Automatic || edge.Relation != providerIndex {
				t.Fatal("lost actual owned-index adjacency", edge)
			}
			ownedIndexes++
		}
		if edge.Column != "" && edge.Relation.Address() == consumer.Address() {
			columns[edge.Address.SubID]++
		}
	}
	if ownedIndexes != 1 || columns[2] != 1 || columns[3] != 1 || columns[5] != 0 {
		t.Fatal("pre-removed owned-index closure omitted/duplicated sibling or consumed independent generation", ownedIndexes, columns)
	}
	e.DiscardCatalogStage(preflight)
	if e.CatalogRead().State() != retained.State() || e.Storage.Relations[consumer.Address()] != store || e.Storage.IndexRoots[providerIndex.Address()] != indexRoot || !storage.SameVersionRoot(store.Versions, rows) || store.Ends != ends {
		t.Fatal("closure discovery mutated genuine catalog/row/index predecessors")
	}
	sessionSQL(t, owner, "DROP TABLE live_provider CASCADE")
	if stored.ReferenceEpochValid(e.CatalogRead(), consumer, captured) || stored.ReferenceEpochValid(e.CatalogRead(), consumer, capturedIndex) {
		t.Fatal("retired provider resurrected")
	}
	sessionSQL(t, owner, "CREATE TEMP TABLE live_provider(v INT)")
	sessionSQL(t, owner, "CREATE INDEX live_provider_index ON live_provider(v)")
	current := e.CatalogRead()
	if lookup(current, "live_provider", owner.SessionToken).Address() == provider.Address() || lookup(current, "live_provider_index", owner.SessionToken).Address() == providerIndex.Address() || stored.ReferenceEpochValid(current, consumer, captured) || stored.ReferenceEpochValid(current, consumer, capturedIndex) {
		t.Fatal("same-name recreated provider rebound old identity")
	}
	for attribute, manifest := range dependencies {
		if !slices.Equal(consumer.ColumnMeta.Generations[attribute].Dependencies, manifest) {
			t.Fatal("cascade/recreation mutated retained original manifest", attribute)
		}
	}
}
