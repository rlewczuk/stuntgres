package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogDropPublicationAndRetainedReads(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE a(id INT)")
	run(t, e, "INSERT INTO a VALUES(11)")
	run(t, e, "CREATE TABLE b(id INT)")
	base := e.CatalogRead()
	a := roleTableRef(t, e, "a")
	b := roleTableRef(t, e, "b")
	retained := run(t, e, "SELECT * FROM a")
	stage := e.BeginCatalogStage(base)
	if err := stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.CatalogRead().Descriptor(a.Address); !ok {
		t.Fatal("private removal leaked")
	}
	if _, ok := e.stageRead(stage).Descriptor(a.Address); ok {
		t.Fatal("staged removal visible")
	}
	run(t, e, "CREATE TABLE unrelated(id INT)")
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changes.Definitions, []ObjectAddress{a.Address}) || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 || changes.Replacement {
		t.Fatal(changes)
	}
	now := e.CatalogRead()
	if now.Epoch() != base.Epoch() {
		t.Fatal("drop advanced epoch")
	}
	if _, ok := now.Descriptor(a.Address); ok || e.storage[a.Address] != nil {
		t.Fatal("drop retained definition/storage")
	}
	oldB, _ := base.Descriptor(b.Address)
	newB, _ := now.Descriptor(b.Address)
	if oldB != newB || roleTableRef(t, e, "b") != b {
		t.Fatal("survivor changed")
	}
	if _, ok := base.Descriptor(a.Address); !ok || retained.Rows.Len() != 1 {
		t.Fatal("retained generation lost")
	}
	retained.Rows.Close()
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: now.Epoch()}); err == nil {
		t.Fatal("publication replay accepted")
	}
	run(t, e, "CREATE TABLE a(other TEXT)")
	replacement := roleTableRef(t, e, "a")
	if replacement.Address.ObjectID <= a.Address.ObjectID || replacement.Epoch != a.Epoch || replacement.owner != a.owner {
		t.Fatal(a, replacement)
	}
}
func TestCatalogDropReferenceAndConflictFences(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE a(id INT)")
	base := e.CatalogRead()
	ref := roleTableRef(t, e, "a")
	for _, bad := range []ObjectRef{{Epoch: ref.Epoch, Address: ref.Address}, {owner: New().owner, Epoch: ref.Epoch, Address: ref.Address}, {owner: ref.owner, Epoch: ref.Epoch, Address: ObjectAddress{ClassID: RelationClassID, ObjectID: ref.Address.ObjectID, SubID: 1}}, {owner: ref.owner, Epoch: ref.Epoch, Address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}, {owner: ref.owner, Epoch: ref.Epoch - 1, Address: ref.Address}} {
		s := e.BeginCatalogStage(base)
		if err := s.Remove(bad); err == nil {
			t.Fatal("invalid ref accepted", bad)
		}
		if len(s.removals) != 0 {
			t.Fatal("failed Remove changed stage")
		}
	}
	s := e.BeginCatalogStage(base)
	if err := s.Remove(ref); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "DROP TABLE a")
	run(t, e, "CREATE TABLE a(id INT)")
	changes, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 {
		t.Fatal(changes, err)
	}
	current := e.CatalogRead()
	fresh := roleTableRef(t, e, "a")
	s = e.BeginCatalogStage(current)
	if err = s.Remove(fresh); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err = e.ValidateCatalogStage(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	changes, err = e.PublishCatalog(p, PublicationToken{Epoch: current.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || roleTableRef(t, e, "a") != fresh {
		t.Fatal(changes, err)
	}
	e.DiscardCatalogStage(s)
	if err = s.Remove(fresh); err == nil {
		t.Fatal("discarded stage accepted")
	}
	s = e.BeginCatalogStage(current)
	if err = s.Remove(fresh); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE bad(id no_such_type)")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: current.Epoch()}); err != nil {
		t.Fatal("failed restore invalidated stage", err)
	}
	run(t, e, "CREATE TABLE a(id INT)")
	fresh = roleTableRef(t, e, "a")
	current = e.CatalogRead()
	s = e.BeginCatalogStage(current)
	if err = s.Remove(fresh); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: current.Epoch()}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
}

func TestCatalogDropChangedDescriptorAndUnrelatedRemoval(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE a(id INT)")
	run(t, e, "CREATE TABLE b(id INT)")
	base := e.CatalogRead()
	a, b := roleTableRef(t, e, "a"), roleTableRef(t, e, "b")
	first, second := e.BeginCatalogStage(base), e.BeginCatalogStage(base)
	if err := first.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := second.Remove(b); err != nil {
		t.Fatal(err)
	}
	pa, err := e.ValidateCatalogStage(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := e.ValidateCatalogStage(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(pa, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(pb, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal("unrelated removal conflicted", err)
	}
	run(t, e, "CREATE TABLE a(id INT)")
	a = roleTableRef(t, e, "a")
	base = e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	if err = stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SetObjectACL(context.Background(), a, ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	changes, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 {
		t.Fatal("changed ACL target lost", changes, err)
	}
	if _, ok := e.CatalogRead().Descriptor(a.Address); !ok {
		t.Fatal("conflict removed table")
	}
}

func TestCatalogDropCanceledGroupAndRestoreEpochFence(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE a(id INT)")
	run(t, e, "CREATE TABLE b(id INT)")
	run(t, e, "INSERT INTO a VALUES(11)")
	run(t, e, "INSERT INTO b VALUES(22)")
	base := e.CatalogRead()
	a, b := roleTableRef(t, e, "a"), roleTableRef(t, e, "b")
	stage := e.BeginCatalogStage(base)
	if err := stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := stage.Remove(b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	changes, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(changes, ChangeSet{}) || e.CatalogRead().state != base.state {
		t.Fatal(changes, err)
	}
	if roleTableRef(t, e, "a") != a || roleTableRef(t, e, "b") != b {
		t.Fatal("canceled group changed refs")
	}
	if tuples(run(t, e, "SELECT * FROM a"))[0][0] != int64(11) || tuples(run(t, e, "SELECT * FROM b"))[0][0] != int64(22) {
		t.Fatal("canceled group lost rows")
	}
	stage = e.BeginCatalogStage(base)
	if err = stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE replacement(id INT); INSERT INTO replacement VALUES(99)")); err != nil {
		t.Fatal(err)
	}
	changes, err = e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, ErrCatalogReplaced) || !reflect.DeepEqual(changes, ChangeSet{}) {
		t.Fatal(changes, err)
	}
	if tuples(run(t, e, "SELECT * FROM replacement"))[0][0] != int64(99) {
		t.Fatal("stale drop damaged replacement")
	}
}

func TestCatalogDropRejectsBuiltinAndCustomProvider(t *testing.T) {
	provider := &initializationTestProvider{}
	e, err := NewWithCatalogProviders(provider)
	if err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	custom := &namespaceDescriptor{address: ObjectAddress{ClassID: provider.ClassID(), ObjectID: 1}, name: "feature", revision: 1, owner: e.roles.names["postgres"]}
	stage := e.BeginCatalogStage(base)
	if err = stage.Create(custom); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	base = e.CatalogRead()
	for _, address := range []ObjectAddress{{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, {ClassID: TypeClassID, ObjectID: 23}, custom.address} {
		stage = e.BeginCatalogStage(base)
		if err = stage.Remove(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: address}); err == nil {
			t.Fatal("unowned class accepted", address)
		}
		if len(stage.removals) != 0 || e.CatalogRead().state != base.state {
			t.Fatal("rejected removal changed authority")
		}
	}
}
