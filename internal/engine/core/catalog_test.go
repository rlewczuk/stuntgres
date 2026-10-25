package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Fixture identities use the real allocator: bootstrap definitions can occupy
// user-range OIDs without becoming relation descriptors.
func catalogUnitOID(t *testing.T, e *core.Engine) ids.OID {
	t.Helper()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestCatalogIdentityAndReplacement(t *testing.T) {
	e := engine.New()
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
	if _, err = enginetest.Execute(e, context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view = e.CatalogRead()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}
	e.Mu.Lock()
	ref, err := e.ResolveReference(enginetest.CatalogAuthorization(e, view), name)
	d, _ := view.Relation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	e.Mu.Unlock()
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
	if _, err = enginetest.Execute(e, context.Background(), q[0], nil); err != nil {
		t.Fatal(err)
	}
	if err = enginetest.ValidateObjectRef(e, view, ref); err != nil {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().Descriptor(ref.Address)
	if current.Revision() != revision {
		t.Fatalf("INSERT changed definition revision from %d to %d", revision, current.Revision())
	}
	other := engine.New()
	if err = enginetest.ValidateObjectRef(other, other.CatalogRead(), ref); err == nil {
		t.Fatal("foreign reference accepted")
	}
	old := view
	if _, err = e.ReplaceCatalog(context.Background(), other, other.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != old.Epoch()+1 {
		t.Fatal("replacement did not advance epoch once")
	}
	if err = enginetest.ValidateObjectRef(e, old, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old ref error: %v", err)
	}
	if _, ok := old.Descriptor(ref.Address); !ok {
		t.Fatal("retained read lost its descriptor")
	}
}

func TestCatalogStageConflictsAndForeignViews(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	makeDesc := func(oid ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
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
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
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
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: e.CatalogRead().Epoch()}); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), conflictB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: e.CatalogRead().Epoch()}); err == nil {
		t.Fatal("same-key stage conflict was not detected")
	}
	foreign := engine.New().CatalogRead()
	bad := e.BeginCatalogStage(foreign)
	if _, err = e.ValidateCatalogStage(context.Background(), bad); err == nil {
		t.Fatal("foreign stage accepted")
	}
	ref := catalogRef(t, e, e.CatalogRead(), "first")
	ref.Address.SubID = 1
	if err = enginetest.ValidateObjectRef(e, foreign, ref); err == nil {
		t.Fatal("foreign read accepted")
	}
}

type testProvider struct{ id ids.OID }

func (p testProvider) ClassID() ids.OID                              { return p.id }
func (testProvider) Validate(catalog.Read, catalog.Descriptor) error { return nil }

// Allocator units are in catalog/store_test.go.
func TestCatalogProviderBoundaries(t *testing.T) {
	e := engine.New()
	if err := e.RegisterCatalogProvider(nil); err == nil {
		t.Fatal("nil provider accepted")
	}
	if err := e.RegisterCatalogProvider(catalog.NamespaceProvider{}); err == nil {
		t.Fatal("duplicate provider accepted")
	}
	if err := e.RegisterCatalogProvider(testProvider{id: 90000}); err == nil {
		t.Fatal("provider registered after initialization")
	}
}

func TestCatalogCanceledStageHasNoPublication(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: "canceled", ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
	d.ColumnMeta.HighWater = 1
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
	e := engine.New()
	stmt, _ := parser.Parse("CREATE TABLE public.p (a INTEGER)")
	if _, err := enginetest.Execute(e, context.Background(), stmt[0], nil); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "p")
	ref.Address.SubID = 1
	if err := enginetest.ValidateObjectRef(e, view, ref); err != nil {
		t.Fatal(err)
	}
	invalid := ref
	invalid.Address.SubID = 2
	if err := enginetest.ValidateObjectRef(e, view, invalid); err == nil {
		t.Fatal("invalid attribute number accepted")
	}
	stage := e.BeginCatalogStage(view)
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE public.bad (x INTEGER); nonsense")); err == nil {
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
		e.Mu.Lock()
		next := catalog.CloneState(e.Catalog.State)
		next.SetEpoch(epoch)
		err := stored.ReownExpressions(e.Catalog.Read(next), next, nil, false, nil)
		if err == nil {
			e.Catalog.PublishLocked(next)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	setEpoch(ids.CatalogEpoch(^uint64(0)))
	if _, err = e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err == nil || err.Error() != "stuntgres: catalog epoch exhausted" {
		t.Fatalf("epoch exhaustion: %v", err)
	}
	setEpoch(view.Epoch())
	if err = engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: view.Epoch()}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old stage after Reset: %v", err)
	}
}

func TestCatalogStageAddressCollision(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	prepare := func(name string) *core.PreparedCatalogPublication {
		t.Helper()
		stage := e.BeginCatalogStage(base)
		d := &catalog.Relation{ObjectAddress: address, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
		d.ColumnMeta.HighWater = 1
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
	if _, err := enginetest.PublishCatalog(e, first, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "INSERT INTO public.first VALUES (77)")
	before := e.CatalogRead()
	changes, err := enginetest.PublishCatalog(e, second, core.PublicationToken{Epoch: base.Epoch()})
	var conflict catalog.Conflict
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
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM public.first")); len(got) != 1 || got[0][0] != int64(77) {
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
			source := engine.New()
			enginetest.Run(t, source, "CREATE TABLE public.t (id INTEGER)")
			target := source
			if !self {
				target = engine.New()
			}
			insert := enginetest.Statement(t, "INSERT INTO public.t VALUES (1)")
			start := make(chan struct{})
			done := make(chan error, 2)
			go func() {
				<-start
				for i := 0; i < 200; i++ {
					if _, err := enginetest.Execute(source, context.Background(), insert, nil); err != nil {
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
			if got := enginetest.Tuples(enginetest.Run(t, target, "SELECT id FROM public.t")); len(got) != 200 {
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(id INT)")
			op, cleanup, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			var ref ids.TxRef
			if explicit {
				ref, err = e.Begin(op, session.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			if _, err = scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(1)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			replacement := e.CatalogRead()
			if explicit {
				if !e.ReplacementMu.TryLock() {
					t.Fatal("explicit statement retained replacement admission")
				}
				e.ReplacementMu.Unlock()
				if _, err = e.ReplaceCatalog(context.Background(), e, replacement); err != nil {
					t.Fatal(err)
				}
				if err = scope.Finish(nil); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatalf("explicit statement escaped epoch fence: %v", err)
				}
				if _, err = e.Rollback(op, ref); err != nil {
					t.Fatal(err)
				}
				if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM t")); len(got) != 0 {
					t.Fatalf("fenced private row published: %v", got)
				}
				return
			}
			if e.ReplacementMu.TryLock() {
				e.ReplacementMu.Unlock()
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
			if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM t")); len(got) != 1 || got[0][0] != int64(1) {
				t.Fatalf("self replacement lost completed implicit row: %v", got)
			}
		})
	}
}
func TestForeignCatalogReplacementOwnsRowBacking(t *testing.T) {
	source, target := engine.New(), engine.New()
	for _, sql := range []string{
		"CREATE TABLE public.t (id INTEGER)",
		"INSERT INTO public.t VALUES (1)",
		"INSERT INTO public.t VALUES (2)",
		"INSERT INTO public.t VALUES (3)",
	} {
		enginetest.Run(t, source, sql)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}

	enginetest.Run(t, source, "INSERT INTO public.t VALUES (40)")
	enginetest.Run(t, target, "INSERT INTO public.t VALUES (50)")

	assertRows := func(eng *core.Engine, want []int64) {
		t.Helper()
		got := enginetest.Tuples(enginetest.Run(t, eng, "SELECT id FROM public.t ORDER BY id"))
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

func catalogRef(t *testing.T, e *core.Engine, view catalog.Read, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	ref, err := e.ResolveReference(enginetest.CatalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestCatalogReferenceProvenance(t *testing.T) {
	a, b := engine.New(), engine.New()
	for i, e := range []*core.Engine{a, b} {
		enginetest.Run(t, e, "CREATE TABLE public.t (id INTEGER)")
		enginetest.Run(t, e, "INSERT INTO public.t VALUES ($1)", int64((i+1)*11))
	}
	va, vb := a.CatalogRead(), b.CatalogRead()
	ra, rb := catalogRef(t, a, va, "t"), catalogRef(t, b, vb, "t")
	if ra.Address.ObjectID == 0 || ra.Address != rb.Address || ra.Epoch != rb.Epoch || ra == rb || ra.Domain() == nil || rb.Domain() == nil {
		t.Fatalf("coincident identities lack provenance: %#v / %#v", ra, rb)
	}
	for _, tc := range []struct {
		engine       *core.Engine
		view         catalog.Read
		own, foreign ids.ObjectRef
		want         int64
	}{{a, va, ra, rb, 11}, {b, vb, rb, ra, 22}} {
		if err := enginetest.ValidateObjectRef(tc.engine, tc.view, tc.own); err != nil {
			t.Fatal(err)
		}
		ownerless := tc.own
		ownerless = ids.ObjectRef{Epoch: ownerless.Epoch, Address: ownerless.Address}
		for _, ref := range []ids.ObjectRef{tc.foreign, ownerless} {
			if err := enginetest.ValidateObjectRef(tc.engine, tc.view, ref); err == nil || err.Error() != "stuntgres: foreign catalog reference" {
				t.Fatalf("foreign reference: %v", err)
			}
		}
		if err := enginetest.ValidateObjectRef(tc.engine, va, tc.own); tc.engine == b && (err == nil || err.Error() != "stuntgres: foreign catalog view") {
			t.Fatalf("foreign view: %v", err)
		}
		if tc.engine.CatalogRead().State() != tc.view.State() || catalogRef(t, tc.engine, tc.view, "t") != tc.own {
			t.Fatal("rejected validation changed authority")
		}
		rows := enginetest.Tuples(enginetest.Run(t, tc.engine, "SELECT id FROM public.t"))
		if len(rows) != 1 || rows[0][0] != tc.want {
			t.Fatalf("instance rows: %v", rows)
		}
	}
}

func TestCatalogReferencesUseCoherentView(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	enginetest.Run(t, e, "CREATE TABLE public.committed (id INTEGER)")
	current := e.CatalogRead()
	ref := catalogRef(t, e, current, "committed")
	if err := enginetest.ValidateObjectRef(e, base, ref); err == nil || err.Error() != "stuntgres: catalog object is missing" {
		t.Fatalf("old same-epoch view adopted later object: %v", err)
	}
	e.Mu.Lock()
	_, err := e.ResolveReference(enginetest.CatalogAuthorization(e, base), parser.ObjectName{Parts: []parser.Identifier{{Text: "committed"}}})
	creation, creationErr := e.CreationNamespace(enginetest.CatalogAuthorization(e, base), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "new"}}})
	e.Mu.Unlock()
	enginetest.State(t, err, "42P01")
	if creationErr != nil || creation.Domain() != e.Catalog.Domain || creation.Epoch != base.Epoch() {
		t.Fatalf("retained creation view: %#v, %v", creation, creationErr)
	}
	stage := e.BeginCatalogStage(base)
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: creation.Address, ObjectName: "private", ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
	d.ColumnMeta.HighWater = 1
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	view := e.StageRead(stage)
	private := catalogRef(t, e, view, "private")
	column := private
	column.Address.SubID = 1
	if err := enginetest.ValidateObjectRef(e, view, column); err != nil {
		t.Fatalf("private-stage column: %v", err)
	}
	if err := enginetest.ValidateObjectRef(e, current, private); err == nil {
		t.Fatal("private-stage ref escaped to committed view")
	}
	column.Address.SubID = 2
	if err := enginetest.ValidateObjectRef(e, view, column); err == nil {
		t.Fatal("invalid private column accepted")
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()})
	if err != nil || len(changes.Definitions) != 1 || changes.Definitions[0] != private.Address {
		t.Fatalf("private publication: %#v, %v", changes, err)
	}
	if catalogRef(t, e, e.CatalogRead(), "private") != private || catalogRef(t, e, current, "committed") != ref {
		t.Fatal("unrelated publication changed same-epoch identity")
	}
}

func TestCatalogReplacementPreservesOwner(t *testing.T) {
	source, target := engine.New(), engine.New()
	enginetest.Run(t, source, "CREATE TABLE public.t (id INTEGER)")
	enginetest.Run(t, source, "INSERT INTO public.t VALUES (99)")
	enginetest.Run(t, target, "CREATE TABLE public.t (id INTEGER)")
	enginetest.Run(t, target, "INSERT INTO public.t VALUES (11)")
	sourceView, old := source.CatalogRead(), target.CatalogRead()
	sourceRef, oldRef := catalogRef(t, source, sourceView, "t"), catalogRef(t, target, old, "t")
	owner := target.Catalog.Domain
	changes, err := target.ReplaceCatalog(context.Background(), source, sourceView)
	if err != nil || !changes.Replacement || target.Catalog.Domain != owner || target.CatalogRead().Epoch() != old.Epoch()+1 {
		t.Fatalf("replacement ownership: %#v, %v", changes, err)
	}
	newRef := catalogRef(t, target, target.CatalogRead(), "t")
	if newRef.Domain() != owner || newRef.Address != sourceRef.Address {
		t.Fatalf("replacement ref: %#v", newRef)
	}
	if err := enginetest.ValidateObjectRef(target, target.CatalogRead(), oldRef); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("old target ref: %v", err)
	}
	if err := enginetest.ValidateObjectRef(target, target.CatalogRead(), sourceRef); err == nil || err.Error() != "stuntgres: foreign catalog reference" {
		t.Fatalf("source adopted by target: %v", err)
	}
	if err := enginetest.ValidateObjectRef(source, sourceView, sourceRef); err != nil {
		t.Fatal(err)
	}
	before := target.CatalogRead()
	stage := target.BeginCatalogStage(before)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(ctx, target, strings.NewReader("CREATE TABLE public.other (id INTEGER)")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	if err := engine.Restore(context.Background(), target, strings.NewReader("nonsense")); err == nil {
		t.Fatal("bad restore accepted")
	}
	if target.Catalog.Domain != owner || target.CatalogRead().State() != before.State() {
		t.Fatal("failed restore changed authority")
	}
	if err := enginetest.ValidateObjectRef(target, before, newRef); err != nil {
		t.Fatal(err)
	}
	prepared, err := target.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(target, prepared, core.PublicationToken{Epoch: before.Epoch()}); err != nil {
		t.Fatalf("failed restore fenced eligible stage: %v", err)
	}
	if err := engine.Reset(context.Background(), target); err != nil || target.Catalog.Domain != owner {
		t.Fatalf("reset ownership: %v", err)
	}
	if err := engine.Reset(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	after := target.CatalogRead()
	for _, receiver := range []*core.Engine{source, target} {
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
	e := engine.New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	d := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectName: "private", ObjectRevision: 1}
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	changes, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || changes.Replacement || e.CatalogRead().State() != base.State() {
		t.Fatalf("canceled publication changed authority: %#v, %v", changes, err)
	}
	e.DiscardCatalogStage(stage)
}

func TestCatalogRejectsMalformedColumnIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		columns []catalog.Column
		index   map[string]int
	}{
		{"missing first name", []catalog.Column{{Name: "id"}}, map[string]int{"other": 0}},
		{"duplicate names", []catalog.Column{{Name: "id"}, {Name: "id"}}, map[string]int{"id": 0, "other": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			base := e.CatalogRead()
			for i := range tc.columns {
				tc.columns[i].Usage = enginetest.Usage(e, sqltype.Int4)
			}
			stage := e.BeginCatalogStage(base)
			attributes := make([]int32, len(tc.columns))
			for i := range attributes {
				attributes[i] = int32(i + 1)
			}
			d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "bad", ObjectRevision: 1, Columns: tc.columns, Index: tc.index, Attributes: attributes}
			d.ColumnMeta.HighWater = int32(len(attributes))
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
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE public.t (id INTEGER)")
	enginetest.Run(t, e, "INSERT INTO public.t VALUES (11)")
	before := e.CatalogRead()
	ref := catalogRef(t, e, before, "t")
	owner := e.Catalog.Domain
	storage := e.Storage.Relations[ref.Address]
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
	if e.Catalog.Domain != owner || e.CatalogRead().State() != before.State() || e.CatalogRead().Epoch() != before.Epoch() || e.Storage.Relations[ref.Address] != storage {
		t.Fatal("canceled self-replacement changed owner/state/epoch/storage")
	}
	if err := enginetest.ValidateObjectRef(e, before, ref); err != nil {
		t.Fatal(err)
	}
	rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM public.t"))
	if len(rows) != 1 || rows[0][0] != int64(11) {
		t.Fatalf("canceled replacement changed rows: %v", rows)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); err != nil {
		t.Fatalf("canceled replacement fenced eligible stage: %v", err)
	}
}

func TestCatalogPublicationSingleUseAndDiscard(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "validated discard", true: "published replay"}[published], func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE public.sentinel (id INTEGER)")
			enginetest.Run(t, e, "INSERT INTO public.sentinel VALUES (11)")
			base := e.CatalogRead()
			stage := e.BeginCatalogStage(base)
			d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "p", ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"id": 0}, Attributes: []int32{1}}
			d.ColumnMeta.HighWater = 1
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if published {
				changes, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()})
				if err != nil || len(changes.Definitions) != 1 || changes.Definitions[0] != d.Address() {
					t.Fatalf("initial publication = %#v, %v", changes, err)
				}
				enginetest.Run(t, e, "INSERT INTO public.p VALUES (22)")
			}
			before := e.CatalogRead()
			sentinel := catalogRef(t, e, before, "sentinel")
			owner := e.Catalog.Domain
			sentinelStorage := e.Storage.Relations[sentinel.Address]
			publishedStorage := e.Storage.Relations[d.Address()]
			storageCount := len(e.Storage.Relations)
			assertUnchanged := func() {
				t.Helper()
				if e.Catalog.Domain != owner || e.CatalogRead().State() != before.State() || e.CatalogRead().Epoch() != before.Epoch() || len(e.Storage.Relations) != storageCount || e.Storage.Relations[sentinel.Address] != sentinelStorage || e.Storage.Relations[d.Address()] != publishedStorage {
					t.Fatal("lifecycle operation changed committed authority/storage")
				}
				rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM public.sentinel"))
				if len(rows) != 1 || rows[0][0] != int64(11) {
					t.Fatalf("sentinel rows changed: %v", rows)
				}
				if published {
					rows = enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM public.p"))
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
				changes, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()})
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
				if published && stage.Lifecycle != core.CatalogPublished || !published && stage.Lifecycle != core.CatalogDiscarded {
					t.Fatalf("discard lifecycle = %v", stage.Lifecycle)
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
func replaceWithFreshCatalog(ctx context.Context, e *core.Engine) (core.ChangeSet, error) {
	fresh := engine.New()
	return e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
}
