package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/parser"
)

func TestCatalogIdentityAndReplacement(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		oid  OID
		name string
	}{{PGCatalogNamespaceOID, "pg_catalog"}, {PublicNamespaceOID, "public"}} {
		a := ObjectAddress{ClassID: NamespaceClassID, ObjectID: tc.oid}
		d, ok := view.Descriptor(a)
		if !ok || d.Name() != tc.name || d.Revision() != 1 {
			t.Fatalf("namespace %s = %#v, %v", tc.name, d, ok)
		}
	}
	stmt, err := parser.Parse("CREATE TABLE public.t (id INTEGER, label TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view = e.CatalogRead()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}
	e.mu.Lock()
	d, ref, storage, err := e.resolveRelation(name)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if d.Address().ObjectID == 0 || ref.Epoch != view.Epoch() || len(d.columns) != 2 || d.attributes[0] != 1 || d.attributes[1] != 2 || storage == nil {
		t.Fatalf("relation identity = %#v, ref=%#v", d, ref)
	}
	q, err := parser.Parse("INSERT INTO public.t VALUES (1,'a')")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(context.Background(), q[0], nil); err != nil {
		t.Fatal(err)
	}
	if err = e.ValidateObjectRef(view, ref); err != nil {
		t.Fatal(err)
	}
	if d.Revision() != 1 {
		t.Fatalf("INSERT changed definition revision to %d", d.Revision())
	}
	other := New()
	if err = other.ValidateObjectRef(other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign reference accepted")
	}
	old := view
	if _, err = e.ReplaceCatalog(context.Background(), other.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != old.Epoch()+1 {
		t.Fatal("replacement did not advance epoch once")
	}
	if err = e.ValidateObjectRef(old, ref); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatalf("old ref error: %v", err)
	}
	if _, ok := old.Descriptor(ref.Address); !ok {
		t.Fatal("retained read lost its descriptor")
	}
}

func TestCatalogStageConflictsAndForeignViews(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	makeDesc := func(oid OID, name string) *relationDescriptor {
		return &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: ns, name: name, revision: 1, columns: []parser.Column{{Name: "id", Type: parser.Int4}}, index: map[string]int{"id": 0}, attributes: []int32{1}}
	}
	first, second := e.BeginCatalogStage(base), e.BeginCatalogStage(base)
	if err := first.Create(makeDesc(16384, "first")); err != nil {
		t.Fatal(err)
	}
	if err := second.Create(makeDesc(16385, "second")); err != nil {
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
	if err = conflictA.Create(makeDesc(16386, "same")); err != nil {
		t.Fatal(err)
	}
	if err = conflictB.Create(makeDesc(16387, "same")); err != nil {
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
	if err = e.ValidateObjectRef(foreign, ObjectRef{Epoch: foreign.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: 16384, SubID: 1}}); err == nil {
		t.Fatal("foreign read accepted")
	}
}

type testProvider struct{ id OID }

func (p testProvider) ClassID() OID                         { return p.id }
func (testProvider) Validate(CatalogRead, Descriptor) error { return nil }

func TestCatalogAllocatorAndProviderBoundaries(t *testing.T) {
	e := New()
	e.nextOID = PGCatalogNamespaceOID
	if n, err := e.allocateOIDLocked(); err != nil || n != PGCatalogNamespaceOID+1 {
		t.Fatalf("reserved OID skip: %d %v", n, err)
	}
	e.nextOID = 0
	if _, err := e.allocateOIDLocked(); err == nil || err.Error() != "stuntgres: catalog object identity exhausted" {
		t.Fatalf("OID exhaustion: %v", err)
	}
	e.nextRevision = DefinitionRevision(^uint64(0))
	if _, err := e.revisionLocked(); err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" {
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
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 16384}, namespace: ns, name: "canceled", revision: 1, columns: []parser.Column{{Name: "id", Type: parser.Int4}}, index: map[string]int{"id": 0}, attributes: []int32{1}}
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stage: %v", err)
	}
	if len(e.CatalogRead().Lookup(NameKey{ClassID: RelationClassID, Namespace: ns, Name: "canceled"})) != 0 {
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
	if _, err := e.Execute(context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	ref := ObjectRef{Epoch: view.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: 16384, SubID: 1}}
	if err := e.ValidateObjectRef(view, ref); err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateObjectRef(view, ObjectRef{Epoch: view.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: 16384, SubID: 2}}); err == nil {
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
	e.mu.Lock()
	e.state = cloneCatalog(e.state)
	e.state.epoch = CatalogEpoch(^uint64(0))
	e.mu.Unlock()
	if _, err = e.ReplaceCatalog(context.Background(), e.CatalogRead()); err == nil || err.Error() != "stuntgres: catalog epoch exhausted" {
		t.Fatalf("epoch exhaustion: %v", err)
	}
	e.mu.Lock()
	e.state = cloneCatalog(e.state)
	e.state.epoch = view.Epoch()
	e.mu.Unlock()
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: view.Epoch()}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatalf("old stage after Reset: %v", err)
	}
}

func TestCatalogStageAddressCollision(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 16384}
	prepare := func(name string) *PreparedCatalogPublication {
		t.Helper()
		stage := e.BeginCatalogStage(base)
		d := &relationDescriptor{address: address, namespace: ns, name: name, revision: 1, columns: []parser.Column{{Name: "id", Type: parser.Int4}}, index: map[string]int{"id": 0}, attributes: []int32{1}}
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
	if after.state != before.state || len(changes.Definitions) != 0 {
		t.Fatal("conflict changed authority or emitted definitions")
	}
	if got := after.Lookup(NameKey{ClassID: RelationClassID, Namespace: ns, Name: "second"}); len(got) != 0 {
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
					if _, err := source.Execute(context.Background(), insert, nil); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			go func() {
				<-start
				for i := 0; i < 200; i++ {
					if _, err := target.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
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
				if _, err := target.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			if got := tuples(run(t, target, "SELECT id FROM public.t")); len(got) != 200 {
				t.Fatalf("replacement lost committed rows: %d", len(got))
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
	if _, err := target.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
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

type initializationTestDescriptor struct {
	address  ObjectAddress
	name     string
	revision DefinitionRevision
}

func (d initializationTestDescriptor) Address() ObjectAddress       { return d.address }
func (d initializationTestDescriptor) Revision() DefinitionRevision { return d.revision }
func (initializationTestDescriptor) Namespace() ObjectAddress       { return ObjectAddress{} }
func (d initializationTestDescriptor) Name() string                 { return d.name }
func (initializationTestDescriptor) Kind() ObjectKind                { return NamespaceObject }

type initializationTestProvider struct{ validated int }

func (*initializationTestProvider) ClassID() OID { return 90000 }
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

	stage := e.BeginCatalogStage(e.CatalogRead())
	descriptor := initializationTestDescriptor{
		address:  ObjectAddress{ClassID: provider.ClassID(), ObjectID: 1},
		name:     "feature",
		revision: 1,
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
	if err := e.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("provider registered after initialization")
	}

	var uninitialized Engine
	if err := uninitialized.RegisterCatalogProvider(&initializationTestProvider{}); err == nil {
		t.Fatal("zero-value Engine accepted registration")
	}
}
