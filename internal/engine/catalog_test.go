package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Fixture identities use the real allocator: bootstrap definitions can occupy
// user-range OIDs without becoming relation descriptors.
func catalogUnitOID(t *testing.T, e *Engine) ids.OID {
	t.Helper()
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestCatalogIdentityAndReplacement(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		oid  ids.OID
		name string
	}{{ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PublicNamespaceOID, "public"}} {
		a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: tc.oid}
		d, ok := view.Descriptor(a)
		if !ok || d.Name() != tc.name {
			t.Fatalf("namespace %s = %#v, %v", tc.name, d, ok)
		}
	}
	stmt, err := parser.Parse("CREATE TABLE public.t (id INTEGER, label TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testExecute(e, context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view = e.CatalogRead()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}
	e.mu.Lock()
	ref, err := e.resolveReference(catalogAuthorization(e, view), name)
	d, _ := view.Relation(ref.Address)
	storage := e.storage[ref.Address]
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if d.Address().ObjectID == 0 || ref.Epoch != view.Epoch() || len(d.Columns) != 2 || d.Attributes[0] != 1 || d.Attributes[1] != 2 || storage == nil {
		t.Fatalf("relation identity = %#v, ref=%#v", d, ref)
	}
	revision := d.Revision()
	q, err := parser.Parse("INSERT INTO public.t VALUES (1,'a')")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testExecute(e, context.Background(), q[0], nil); err != nil {
		t.Fatal(err)
	}
	if err = e.ValidateObjectRef(view, ref); err != nil {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().Descriptor(ref.Address)
	if current.Revision() != revision {
		t.Fatalf("INSERT changed definition revision from %d to %d", revision, current.Revision())
	}
	other := New()
	if err = other.ValidateObjectRef(other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign reference accepted")
	}
	old := view
	if _, err = e.ReplaceCatalog(context.Background(), other, other.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != old.Epoch()+1 {
		t.Fatal("replacement did not advance epoch once")
	}
	if err = e.ValidateObjectRef(old, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old ref error: %v", err)
	}
	if _, ok := old.Descriptor(ref.Address); !ok {
		t.Fatal("retained read lost its descriptor")
	}
}

func TestCatalogStageConflictsAndForeignViews(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	makeDesc := func(oid ids.OID, name string) *relationDescriptor {
		return &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}, AttributeHighWater: 1}
	}
	first, second := e.BeginCatalogStage(base), e.BeginCatalogStage(base)
	if err := first.Create(makeDesc(catalogUnitOID(t, e), "first")); err != nil {
		t.Fatal(err)
	}
	if err := second.Create(makeDesc(catalogUnitOID(t, e), "second")); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatalf("unrelated-key stage conflicted: %v", err)
	}
	conflictA, conflictB := e.BeginCatalogStage(e.CatalogRead()), e.BeginCatalogStage(e.CatalogRead())
	if err = conflictA.Create(makeDesc(catalogUnitOID(t, e), "same")); err != nil {
		t.Fatal(err)
	}
	if err = conflictB.Create(makeDesc(catalogUnitOID(t, e), "same")); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), conflictA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: e.CatalogRead().Epoch()}); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), conflictB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: e.CatalogRead().Epoch()}); err == nil {
		t.Fatal("same-key stage conflict was not detected")
	}
	foreign := New().CatalogRead()
	bad := e.BeginCatalogStage(foreign)
	if _, err = e.ValidateCatalogStage(context.Background(), bad); err == nil {
		t.Fatal("foreign stage accepted")
	}
	ref := catalogRef(t, e, e.CatalogRead(), "first")
	ref.Address.SubID = 1
	if err = e.ValidateObjectRef(foreign, ref); err == nil {
		t.Fatal("foreign read accepted")
	}
}

type testProvider struct{ id ids.OID }

func (p testProvider) ClassID() ids.OID                     { return p.id }
func (testProvider) Validate(CatalogRead, Descriptor) error { return nil }

func TestCatalogAllocatorAndProviderBoundaries(t *testing.T) {
	e := New()
	e.catalog.nextOID = ids.PGCatalogNamespaceOID
	if n, err := e.catalog.allocateOIDLocked(); err != nil || n != ids.PGCatalogNamespaceOID+1 {
		t.Fatalf("reserved OID skip: %d %v", n, err)
	}
	for _, class := range []ids.OID{ids.NamespaceClassID, ids.RelationClassID} {
		t.Run(fmt.Sprint(class), func(t *testing.T) {
			e := New()
			last := ^ids.OID(0)
			address := ids.ObjectAddress{ClassID: class, ObjectID: last}
			// Exercise terminal occupancy through the allocator's actual catalog
			// seam; exhaustion must not wrap, reuse or publish anything.
			e.catalog.state = catalog.CloneState(e.catalog.state)
			if class == ids.NamespaceClassID {
				e.catalog.state.SetDescriptor(address, &namespaceDescriptor{ObjectAddress: address, ObjectName: "last", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")})
			} else {
				e.catalog.state.SetDescriptor(address, &relationDescriptor{ObjectAddress: address, ObjectName: "last", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}})
			}
			before := e.catalog.state
			e.catalog.nextOID = last
			if _, err := e.catalog.allocateOIDLocked(); err == nil || err.Error() != "stuntgres: catalog object identity exhausted" || e.catalog.nextOID != 0 || e.catalog.state != before {
				t.Fatal("occupied terminal OID did not exhaust without publication", err)
			}
			if _, err := e.catalog.allocateOIDLocked(); err == nil || e.catalog.nextOID != 0 || e.catalog.state != before {
				t.Fatal("exhausted allocator reused identity", err)
			}
		})
	}
	e.catalog.nextOID = 0
	if _, err := e.catalog.allocateOIDLocked(); err == nil || err.Error() != "stuntgres: catalog object identity exhausted" {
		t.Fatalf("OID exhaustion: %v", err)
	}
	e.catalog.nextRevision = ids.DefinitionRevision(^uint64(0))
	if _, err := e.catalog.revisionLocked(); err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" {
		t.Fatalf("revision exhaustion: %v", err)
	}
	if err := e.RegisterCatalogProvider(nil); err == nil {
		t.Fatal("nil provider accepted")
	}
	if err := e.RegisterCatalogProvider(namespaceProvider{}); err == nil {
		t.Fatal("duplicate provider accepted")
	}
	if err := e.RegisterCatalogProvider(testProvider{id: 90000}); err == nil {
		t.Fatal("provider registered after initialization")
	}
}

func TestCatalogCanceledStageHasNoPublication(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	d := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: "canceled", ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
	d.AttributeHighWater = 1
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stage: %v", err)
	}
	if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "canceled"})) != 0 {
		t.Fatal("canceled stage published")
	}
	e.DiscardCatalogStage(stage)
	if err := stage.Create(d); err == nil {
		t.Fatal("discarded stage reused")
	}
}
func TestCatalogEpochFailureAndSubobjectValidation(t *testing.T) {
	e := New()
	stmt, _ := parser.Parse("CREATE TABLE public.p (a INTEGER)")
	if _, err := testExecute(e, context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "p")
	ref.Address.SubID = 1
	if err := e.ValidateObjectRef(view, ref); err != nil {
		t.Fatal(err)
	}
	invalid := ref
	invalid.Address.SubID = 2
	if err := e.ValidateObjectRef(view, invalid); err == nil {
		t.Fatal("invalid attribute number accepted")
	}
	stage := e.BeginCatalogStage(view)
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE public.bad (x INTEGER); nonsense")); err == nil {
		t.Fatal("malformed restore accepted")
	}
	if e.CatalogRead().Epoch() != view.Epoch() {
		t.Fatal("failed Restore changed epoch")
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	setEpoch := func(epoch ids.CatalogEpoch) {
		e.mu.Lock()
		next := catalog.CloneState(e.catalog.state)
		next.SetEpoch(epoch)
		err := e.reownCatalogExpressions(next, nil, false)
		if err == nil {
			e.catalog.state = next
		}
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	setEpoch(ids.CatalogEpoch(^uint64(0)))
	if _, err = e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err == nil || err.Error() != "stuntgres: catalog epoch exhausted" {
		t.Fatalf("epoch exhaustion: %v", err)
	}
	setEpoch(view.Epoch())
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: view.Epoch()}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old stage after Reset: %v", err)
	}
}

func TestCatalogStageAddressCollision(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	prepare := func(name string) *PreparedCatalogPublication {
		t.Helper()
		stage := e.BeginCatalogStage(base)
		d := &relationDescriptor{ObjectAddress: address, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
		d.AttributeHighWater = 1
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
		p, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, second := prepare("first"), prepare("second")
	if _, err := e.PublishCatalog(first, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	run(t, e, "INSERT INTO public.first VALUES (77)")
	before := e.CatalogRead()
	changes, err := e.PublishCatalog(second, PublicationToken{Epoch: base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("address collision: changes=%+v error=%v", changes, err)
	}
	after := e.CatalogRead()
	if after.State() != before.State() || len(changes.Definitions) != 0 {
		t.Fatal("conflict changed authority or emitted definitions")
	}
	if got := after.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "second"}); len(got) != 0 {
		t.Fatalf("conflicting name installed: %v", got)
	}
	d, ok := after.Descriptor(address)
	if !ok || d.Name() != "first" {
		t.Fatalf("original descriptor overwritten: %v", d)
	}
	if got := tuples(run(t, e, "SELECT id FROM public.first")); len(got) != 1 || got[0][0] != int64(77) {
		t.Fatalf("original storage changed: %v", got)
	}
}

func TestCatalogConcurrentReplacement(t *testing.T) {
	for _, self := range []bool{true, false} {
		name := "foreign"
		if self {
			name = "self"
		}
		t.Run(name, func(t *testing.T) {
			source := New()
			run(t, source, "CREATE TABLE public.t (id INTEGER)")
			target := source
			if !self {
				target = New()
			}
			insert := statement(t, "INSERT INTO public.t VALUES (1)")
			start := make(chan struct{})
			done := make(chan error, 2)
			go func() {
				<-start
				for i := 0; i < 200; i++ {
					if _, err := testExecute(source, context.Background(), insert, nil); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			go func() {
				<-start
				for i := 0; i < 200; i++ {
					if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			close(start)
			for i := 0; i < 2; i++ {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			if !self {
				if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			if got := tuples(run(t, target, "SELECT id FROM public.t")); len(got) != 200 {
				t.Fatalf("replacement lost committed rows: %d", len(got))
			}
		})
	}
}

func TestCatalogReplacementStatementBoundary(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "implicit"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(id INT)")
			op, cleanup, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			var ref ids.TxRef
			if explicit {
				ref, err = e.Begin(op, TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			if _, err = scope.Execute(statement(t, "INSERT INTO t VALUES(1)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			replacement := e.CatalogRead()
			if explicit {
				if !e.replacementMu.TryLock() {
					t.Fatal("explicit statement retained replacement admission")
				}
				e.replacementMu.Unlock()
				if _, err = e.ReplaceCatalog(context.Background(), e, replacement); err != nil {
					t.Fatal(err)
				}
				if err = scope.Finish(nil); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatalf("explicit statement escaped epoch fence: %v", err)
				}
				if _, err = e.Rollback(op, ref); err != nil {
					t.Fatal(err)
				}
				if got := tuples(run(t, e, "SELECT id FROM t")); len(got) != 0 {
					t.Fatalf("fenced private row published: %v", got)
				}
				return
			}
			if e.replacementMu.TryLock() {
				e.replacementMu.Unlock()
				t.Fatal("implicit statement did not retain replacement admission")
			}
			done := make(chan error, 1)
			go func() {
				_, err := e.ReplaceCatalog(context.Background(), e, replacement)
				done <- err
			}()
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Epoch() != replacement.Epoch()+1 {
				t.Fatal("replacement did not advance epoch once")
			}
			if got := tuples(run(t, e, "SELECT id FROM t")); len(got) != 1 || got[0][0] != int64(1) {
				t.Fatalf("self replacement lost completed implicit row: %v", got)
			}
		})
	}
}
func TestForeignCatalogReplacementOwnsRowBacking(t *testing.T) {
	source, target := New(), New()
	for _, sql := range []string{
		"CREATE TABLE public.t (id INTEGER)",
		"INSERT INTO public.t VALUES (1)",
		"INSERT INTO public.t VALUES (2)",
		"INSERT INTO public.t VALUES (3)",
	} {
		run(t, source, sql)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}

	run(t, source, "INSERT INTO public.t VALUES (40)")
	run(t, target, "INSERT INTO public.t VALUES (50)")

	assertRows := func(engine *Engine, want []int64) {
		t.Helper()
		got := tuples(run(t, engine, "SELECT id FROM public.t ORDER BY id"))
		if len(got) != len(want) {
			t.Fatalf("row count = %d, want %d (%v)", len(got), len(want), got)
		}
		for i, value := range want {
			if got[i][0] != value {
				t.Fatalf("rows = %v, want ids %v", got, want)
			}
		}
	}
	assertRows(source, []int64{1, 2, 3, 40})
	assertRows(target, []int64{1, 2, 3, 50})
}

type initializationTestProvider struct{ validated int }

func (*initializationTestProvider) ClassID() ids.OID { return 90000 }
func (p *initializationTestProvider) Validate(_ CatalogRead, d Descriptor) error {
	if d.Address().ClassID != p.ClassID() {
		return errors.New("unexpected descriptor class")
	}
	p.validated++
	return nil
}

func TestCatalogProviderRegistersBeforeInitialization(t *testing.T) {
	provider := &initializationTestProvider{}
	e, err := NewWithCatalogProviders(provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider.validated != 0 {
		t.Fatalf("custom provider received unexpected builtin callbacks: %d", provider.validated)
	}

	owner, err := e.ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	descriptor := &namespaceDescriptor{
		ObjectAddress:  ids.ObjectAddress{ClassID: provider.ClassID(), ObjectID: 1},
		ObjectName:     "feature",
		ObjectRevision: 1,
		Owner:          owner,
	}
	if err := stage.Create(descriptor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatalf("registered provider could not validate its descriptor: %v", err)
	}
	if provider.validated != 1 {
		t.Fatalf("provider validations = %d, want 1", provider.validated)
	}
	foreignOwner, err := New().ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidOwner := range []authorization.PrincipalRef{{}, foreignOwner} {
		invalid := *descriptor
		invalid.Owner = invalidOwner
		invalidStage := e.BeginCatalogStage(e.CatalogRead())
		if err := invalidStage.Create(&invalid); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(context.Background(), invalidStage); err == nil {
			t.Fatal("provider descriptor accepted zero or foreign owner")
		}
		e.DiscardCatalogStage(invalidStage)
	}
	if err := e.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("provider registered after initialization")
	}

	var uninitialized Engine
	if err := uninitialized.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("zero-value Engine accepted registration")
	}
}

func catalogRef(t *testing.T, e *Engine, view CatalogRead, name string) ids.ObjectRef {
	t.Helper()
	e.mu.Lock()
	ref, err := e.resolveReference(catalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestCatalogReferenceProvenance(t *testing.T) {
	a, b := New(), New()
	for i, e := range []*Engine{a, b} {
		run(t, e, "CREATE TABLE public.t (id INTEGER)")
		run(t, e, "INSERT INTO public.t VALUES ($1)", int64((i+1)*11))
	}
	va, vb := a.CatalogRead(), b.CatalogRead()
	ra, rb := catalogRef(t, a, va, "t"), catalogRef(t, b, vb, "t")
	if ra.Address.ObjectID == 0 || ra.Address != rb.Address || ra.Epoch != rb.Epoch || ra == rb || ra.Domain() == nil || rb.Domain() == nil {
		t.Fatalf("coincident identities lack provenance: %#v / %#v", ra, rb)
	}
	for _, tc := range []struct {
		engine       *Engine
		view         CatalogRead
		own, foreign ids.ObjectRef
		want         int64
	}{{a, va, ra, rb, 11}, {b, vb, rb, ra, 22}} {
		if err := tc.engine.ValidateObjectRef(tc.view, tc.own); err != nil {
			t.Fatal(err)
		}
		ownerless := tc.own
		ownerless = ids.ObjectRef{Epoch: ownerless.Epoch, Address: ownerless.Address}
		for _, ref := range []ids.ObjectRef{tc.foreign, ownerless} {
			if err := tc.engine.ValidateObjectRef(tc.view, ref); err == nil || err.Error() != "stuntgres: foreign catalog reference" {
				t.Fatalf("foreign reference: %v", err)
			}
		}
		if err := tc.engine.ValidateObjectRef(va, tc.own); tc.engine == b && (err == nil || err.Error() != "stuntgres: foreign catalog view") {
			t.Fatalf("foreign view: %v", err)
		}
		if tc.engine.CatalogRead().State() != tc.view.State() || catalogRef(t, tc.engine, tc.view, "t") != tc.own {
			t.Fatal("rejected validation changed authority")
		}
		rows := tuples(run(t, tc.engine, "SELECT id FROM public.t"))
		if len(rows) != 1 || rows[0][0] != tc.want {
			t.Fatalf("instance rows: %v", rows)
		}
	}
}

func TestCatalogReferencesUseCoherentView(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	run(t, e, "CREATE TABLE public.committed (id INTEGER)")
	current := e.CatalogRead()
	ref := catalogRef(t, e, current, "committed")
	if err := e.ValidateObjectRef(base, ref); err == nil || err.Error() != "stuntgres: catalog object is missing" {
		t.Fatalf("old same-epoch view adopted later object: %v", err)
	}
	e.mu.Lock()
	_, err := e.resolveReference(catalogAuthorization(e, base), parser.ObjectName{Parts: []parser.Identifier{{Text: "committed"}}})
	creation, creationErr := e.creationNamespace(catalogAuthorization(e, base), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "new"}}})
	e.mu.Unlock()
	state(t, err, "42P01")
	if creationErr != nil || creation.Domain() != e.catalog.Domain || creation.Epoch != base.Epoch() {
		t.Fatalf("retained creation view: %#v, %v", creation, creationErr)
	}
	stage := e.BeginCatalogStage(base)
	d := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: creation.Address, ObjectName: "private", ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
	d.AttributeHighWater = 1
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	view := e.stageRead(stage)
	private := catalogRef(t, e, view, "private")
	column := private
	column.Address.SubID = 1
	if err := e.ValidateObjectRef(view, column); err != nil {
		t.Fatalf("private-stage column: %v", err)
	}
	if err := e.ValidateObjectRef(current, private); err == nil {
		t.Fatal("private-stage ref escaped to committed view")
	}
	column.Address.SubID = 2
	if err := e.ValidateObjectRef(view, column); err == nil {
		t.Fatal("invalid private column accepted")
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
	if err != nil || len(changes.Definitions) != 1 || changes.Definitions[0] != private.Address {
		t.Fatalf("private publication: %#v, %v", changes, err)
	}
	if catalogRef(t, e, e.CatalogRead(), "private") != private || catalogRef(t, e, current, "committed") != ref {
		t.Fatal("unrelated publication changed same-epoch identity")
	}
}

func TestCatalogReplacementPreservesOwner(t *testing.T) {
	source, target := New(), New()
	run(t, source, "CREATE TABLE public.t (id INTEGER)")
	run(t, source, "INSERT INTO public.t VALUES (99)")
	run(t, target, "CREATE TABLE public.t (id INTEGER)")
	run(t, target, "INSERT INTO public.t VALUES (11)")
	sourceView, old := source.CatalogRead(), target.CatalogRead()
	sourceRef, oldRef := catalogRef(t, source, sourceView, "t"), catalogRef(t, target, old, "t")
	owner := target.catalog.Domain
	changes, err := target.ReplaceCatalog(context.Background(), source, sourceView)
	if err != nil || !changes.Replacement || target.catalog.Domain != owner || target.CatalogRead().Epoch() != old.Epoch()+1 {
		t.Fatalf("replacement ownership: %#v, %v", changes, err)
	}
	newRef := catalogRef(t, target, target.CatalogRead(), "t")
	if newRef.Domain() != owner || newRef.Address != sourceRef.Address {
		t.Fatalf("replacement ref: %#v", newRef)
	}
	if err := target.ValidateObjectRef(target.CatalogRead(), oldRef); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old target ref: %v", err)
	}
	if err := target.ValidateObjectRef(target.CatalogRead(), sourceRef); err == nil || err.Error() != "stuntgres: foreign catalog reference" {
		t.Fatalf("source adopted by target: %v", err)
	}
	if err := source.ValidateObjectRef(sourceView, sourceRef); err != nil {
		t.Fatal(err)
	}
	before := target.CatalogRead()
	stage := target.BeginCatalogStage(before)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := target.Restore(ctx, strings.NewReader("CREATE TABLE public.other (id INTEGER)")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	if err := target.Restore(context.Background(), strings.NewReader("nonsense")); err == nil {
		t.Fatal("bad restore accepted")
	}
	if target.catalog.Domain != owner || target.CatalogRead().State() != before.State() {
		t.Fatal("failed restore changed authority")
	}
	if err := target.ValidateObjectRef(before, newRef); err != nil {
		t.Fatal(err)
	}
	prepared, err := target.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); err != nil {
		t.Fatalf("failed restore fenced eligible stage: %v", err)
	}
	if err := target.Reset(context.Background()); err != nil || target.catalog.Domain != owner {
		t.Fatalf("reset ownership: %v", err)
	}
	if err := source.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := target.CatalogRead()
	for _, receiver := range []*Engine{source, target} {
		stateBefore := receiver.CatalogRead()
		if _, err := receiver.ReplaceCatalog(context.Background(), source, sourceView); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("stale source replacement: %v", err)
		}
		if receiver.CatalogRead().State() != stateBefore.State() {
			t.Fatal("stale source changed target")
		}
	}
	if target.CatalogRead().State() != after.State() {
		t.Fatal("stale replacement changed target authority")
	}
	if _, ok := sourceView.Descriptor(sourceRef.Address); !ok {
		t.Fatal("stale source view lost readable descriptor")
	}
}

func TestCatalogCanceledPreparedPublication(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	d := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectName: "private", ObjectRevision: 1}
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || changes.Replacement || e.CatalogRead().State() != base.State() {
		t.Fatalf("canceled publication changed authority: %#v, %v", changes, err)
	}
	e.DiscardCatalogStage(stage)
}

func TestCatalogRejectsMalformedColumnIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		columns []ColumnDescriptor
		index   map[string]int
	}{
		{"missing first name", []ColumnDescriptor{{Name: "id"}}, map[string]int{"other": 0}},
		{"duplicate names", []ColumnDescriptor{{Name: "id"}, {Name: "id"}}, map[string]int{"id": 0, "other": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			base := e.CatalogRead()
			for i := range tc.columns {
				tc.columns[i].Usage = testUsage(e, sqltype.Int4)
			}
			stage := e.BeginCatalogStage(base)
			attributes := make([]int32, len(tc.columns))
			for i := range attributes {
				attributes[i] = int32(i + 1)
			}
			d := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "bad", ObjectRevision: 1, Columns: tc.columns, Index: tc.index, Attributes: attributes}
			d.AttributeHighWater = int32(len(attributes))
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
			if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || err.Error() != "stuntgres: invalid relation column identity" {
				t.Fatalf("malformed column index: %v", err)
			}
			if e.CatalogRead().State() != base.State() {
				t.Fatal("invalid descriptor changed committed state")
			}
		})
	}
}

// cancelAfterPreflightContext delegates to a real cancellable context. Its
// second successful Err observation cancels immediately after observing nil,
// deterministically placing cancellation between preflight and row preparation.
// It neither fabricates an error nor changes the underlying Done semantics.
type cancelAfterPreflightContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAfterPreflightContext) Err() error {
	err := c.Context.Err()
	c.checks++
	if c.checks == 2 && err == nil {
		c.cancel()
	}
	return err
}

func TestCatalogSelfReplacementCanceledDuringPreparation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE public.t (id INTEGER)")
	run(t, e, "INSERT INTO public.t VALUES (11)")
	before := e.CatalogRead()
	ref := catalogRef(t, e, before, "t")
	owner := e.catalog.Domain
	storage := e.storage[ref.Address]
	stage := e.BeginCatalogStage(before)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	preflight := &cancelAfterPreflightContext{Context: ctx, cancel: cancel}
	changes, err := e.ReplaceCatalog(preflight, e, before)
	if ctx.Err() != context.Canceled {
		t.Fatal("preparation boundary did not cancel real context")
	}
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 || changes.Replacement {
		t.Fatalf("canceled self-replacement: changes=%#v error=%v", changes, err)
	}
	if e.catalog.Domain != owner || e.CatalogRead().State() != before.State() || e.CatalogRead().Epoch() != before.Epoch() || e.storage[ref.Address] != storage {
		t.Fatal("canceled self-replacement changed owner/state/epoch/storage")
	}
	if err := e.ValidateObjectRef(before, ref); err != nil {
		t.Fatal(err)
	}
	rows := tuples(run(t, e, "SELECT id FROM public.t"))
	if len(rows) != 1 || rows[0][0] != int64(11) {
		t.Fatalf("canceled replacement changed rows: %v", rows)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); err != nil {
		t.Fatalf("canceled replacement fenced eligible stage: %v", err)
	}
}

func TestCatalogPublicationSingleUseAndDiscard(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "validated discard", true: "published replay"}[published], func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE public.sentinel (id INTEGER)")
			run(t, e, "INSERT INTO public.sentinel VALUES (11)")
			base := e.CatalogRead()
			stage := e.BeginCatalogStage(base)
			d := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "p", ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
			d.AttributeHighWater = 1
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if published {
				changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
				if err != nil || len(changes.Definitions) != 1 || changes.Definitions[0] != d.Address() {
					t.Fatalf("initial publication = %#v, %v", changes, err)
				}
				run(t, e, "INSERT INTO public.p VALUES (22)")
			}
			before := e.CatalogRead()
			sentinel := catalogRef(t, e, before, "sentinel")
			owner := e.catalog.Domain
			sentinelStorage := e.storage[sentinel.Address]
			publishedStorage := e.storage[d.Address()]
			storageCount := len(e.storage)
			assertUnchanged := func() {
				t.Helper()
				if e.catalog.Domain != owner || e.CatalogRead().State() != before.State() || e.CatalogRead().Epoch() != before.Epoch() || len(e.storage) != storageCount || e.storage[sentinel.Address] != sentinelStorage || e.storage[d.Address()] != publishedStorage {
					t.Fatal("lifecycle operation changed committed authority/storage")
				}
				rows := tuples(run(t, e, "SELECT id FROM public.sentinel"))
				if len(rows) != 1 || rows[0][0] != int64(11) {
					t.Fatalf("sentinel rows changed: %v", rows)
				}
				if published {
					rows = tuples(run(t, e, "SELECT id FROM public.p"))
					if len(rows) != 1 || rows[0][0] != int64(22) {
						t.Fatalf("published rows changed: %v", rows)
					}
				} else if _, exists := before.Descriptor(d.Address()); exists || len(before.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()})) != 0 {
					t.Fatal("discarded descriptor/name was published")
				}
			}
			assertOrdinaryError := func(err error) {
				t.Helper()
				var sqlError interface{ SQLState() string }
				if err == nil || errors.As(err, &sqlError) {
					t.Fatalf("want ordinary lifecycle error, got %v", err)
				}
			}
			rejectPublication := func() {
				t.Helper()
				changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
				assertOrdinaryError(err)
				if len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 || changes.Replacement {
					t.Fatalf("rejected publication emitted changes: %#v", changes)
				}
				assertUnchanged()
			}
			if published {
				rejectPublication()
			}
			for range 2 {
				e.DiscardCatalogStage(stage)
				if published && stage.lifecycle != catalogPublished || !published && stage.lifecycle != catalogDiscarded {
					t.Fatalf("discard lifecycle = %v", stage.lifecycle)
				}
				assertUnchanged()
			}
			rejectPublication()
			assertOrdinaryError(stage.Create(d))
			_, err = e.ValidateCatalogStage(context.Background(), stage)
			assertOrdinaryError(err)
			assertUnchanged()
		})
	}
}

// replaceWithFreshCatalog replaces e's catalog with a new engine's.
func replaceWithFreshCatalog(ctx context.Context, e *Engine) (ChangeSet, error) {
	fresh := New()
	return e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
}
