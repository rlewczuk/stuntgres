package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestCatalogDropPublicationAndRetainedReads(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	enginetest.Run(t, e, "INSERT INTO a VALUES(11)")
	enginetest.Run(t, e, "CREATE TABLE b(id INT)")
	base := e.CatalogRead()
	a := enginetest.RoleTableRef(t, e, "a")
	b := enginetest.RoleTableRef(t, e, "b")
	row := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.MustRelation(a.Address).RowType)})
	array := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	retained := enginetest.Run(t, e, "SELECT * FROM a")
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
	if _, ok := e.StageRead(stage).Descriptor(a.Address); ok {
		t.Fatal("staged removal visible")
	}
	enginetest.Run(t, e, "CREATE TABLE unrelated(id INT)")
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Definitions) != 3 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 || changes.Replacement {
		t.Fatal(changes)
	}
	for _, address := range []ids.ObjectAddress{a.Address, row.Address(), array.Address()} {
		found := false
		for _, changed := range changes.Definitions {
			found = found || changed == address
		}
		if !found {
			t.Fatal("missing removal closure member", address, changes)
		}
	}
	now := e.CatalogRead()
	if now.Epoch() != base.Epoch() {
		t.Fatal("drop advanced epoch")
	}
	if _, ok := now.Descriptor(a.Address); ok || e.Storage.Relations[a.Address] != nil {
		t.Fatal("drop retained definition/storage")
	}
	for _, typ := range []*catalog.Type{row, array} {
		if _, exists := now.Descriptor(typ.Address()); exists || len(now.Lookup(catalog.DescriptorNameKey(typ))) != 0 {
			t.Fatal("drop retained row/array identity", typ.Address())
		}
		if retained, exists := base.Descriptor(typ.Address()); !exists || retained != typ {
			t.Fatal("retained row/array generation lost", typ.Address())
		}
	}
	oldB, _ := base.Descriptor(b.Address)
	newB, _ := now.Descriptor(b.Address)
	if oldB != newB || enginetest.RoleTableRef(t, e, "b") != b {
		t.Fatal("survivor changed")
	}
	if _, ok := base.Descriptor(a.Address); !ok || retained.Count != 1 {
		t.Fatal("retained generation lost")
	}
	retained.Rows.Close()
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: now.Epoch()}); err == nil {
		t.Fatal("publication replay accepted")
	}
	enginetest.Run(t, e, "CREATE TABLE a(other TEXT)")
	replacement := enginetest.RoleTableRef(t, e, "a")
	if replacement.Address.ObjectID <= a.Address.ObjectID || replacement.Epoch != a.Epoch || replacement.Domain() != a.Domain() {
		t.Fatal(a, replacement)
	}
}
func TestCatalogDropReferenceAndConflictFences(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	base := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "a")
	for _, bad := range []ids.ObjectRef{{Epoch: ref.Epoch, Address: ref.Address}, engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address), ref.Domain().Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ref.Address.ObjectID, SubID: 1}), ref.Domain().Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}), ref.Domain().Ref(ref.Epoch-1, ref.Address)} {
		s := e.BeginCatalogStage(base)
		if err := s.Remove(bad); err == nil {
			t.Fatal("invalid ref accepted", bad)
		}
		if s.RemovalCount() != 0 {
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
	enginetest.Run(t, e, "DROP TABLE a")
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	changes, err := enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 {
		t.Fatal(changes, err)
	}
	current := e.CatalogRead()
	fresh := enginetest.RoleTableRef(t, e, "a")
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
	changes, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: current.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || enginetest.RoleTableRef(t, e, "a") != fresh {
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
	if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE bad(id no_such_type)")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: current.Epoch()}); err != nil {
		t.Fatal("failed restore invalidated stage", err)
	}
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	fresh = enginetest.RoleTableRef(t, e, "a")
	current = e.CatalogRead()
	s = e.BeginCatalogStage(current)
	if err = s.Remove(fresh); err != nil {
		t.Fatal(err)
	}
	p, err = e.ValidateCatalogStage(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: current.Epoch()}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
}

func TestCatalogDropChangedDescriptorAndUnrelatedRemoval(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	enginetest.Run(t, e, "CREATE TABLE b(id INT)")
	base := e.CatalogRead()
	a, b := enginetest.RoleTableRef(t, e, "a"), enginetest.RoleTableRef(t, e, "b")
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
	if _, err = enginetest.PublishCatalog(e, pa, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, pb, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal("unrelated removal conflicted", err)
	}
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	a = enginetest.RoleTableRef(t, e, "a")
	base = e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	if err = stage.Remove(a); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = enginetest.SetObjectACL(e, context.Background(), a, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	changes, err := enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 {
		t.Fatal("changed ACL target lost", changes, err)
	}
	if _, ok := e.CatalogRead().Descriptor(a.Address); !ok {
		t.Fatal("conflict removed table")
	}
}

func TestCatalogDropCanceledGroupAndRestoreEpochFence(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE a(id INT)")
	enginetest.Run(t, e, "CREATE TABLE b(id INT)")
	enginetest.Run(t, e, "INSERT INTO a VALUES(11)")
	enginetest.Run(t, e, "INSERT INTO b VALUES(22)")
	base := e.CatalogRead()
	a, b := enginetest.RoleTableRef(t, e, "a"), enginetest.RoleTableRef(t, e, "b")
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
	changes, err := enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(changes, core.ChangeSet{}) || e.CatalogRead().State() != base.State() {
		t.Fatal(changes, err)
	}
	if enginetest.RoleTableRef(t, e, "a") != a || enginetest.RoleTableRef(t, e, "b") != b {
		t.Fatal("canceled group changed refs")
	}
	if enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM a"))[0][0] != int64(11) || enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM b"))[0][0] != int64(22) {
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
	if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE replacement(id INT); INSERT INTO replacement VALUES(99)")); err != nil {
		t.Fatal(err)
	}
	changes, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, ids.ErrCatalogReplaced) || !reflect.DeepEqual(changes, core.ChangeSet{}) {
		t.Fatal(changes, err)
	}
	if enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM replacement"))[0][0] != int64(99) {
		t.Fatal("stale drop damaged replacement")
	}
}

func TestCatalogDropRejectsBuiltinAndCustomProvider(t *testing.T) {
	provider := &initializationTestProvider{}
	e, err := engine.NewWithCatalogProviders(provider)
	if err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	custom := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: provider.ClassID(), ObjectID: 1}, ObjectName: "feature", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	stage := e.BeginCatalogStage(base)
	if err = stage.Create(custom); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	base = e.CatalogRead()
	for _, address := range []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, {ClassID: ids.TypeClassID, ObjectID: 23}, custom.Address()} {
		stage = e.BeginCatalogStage(base)
		if err = stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err == nil {
			t.Fatal("unowned class accepted", address)
		}
		if stage.RemovalCount() != 0 || e.CatalogRead().State() != base.State() {
			t.Fatal("rejected removal changed authority")
		}
	}
}

type initializationTestProvider struct{ validated int }

func (*initializationTestProvider) ClassID() ids.OID { return 90000 }

func (p *initializationTestProvider) Validate(_ catalog.Read, d catalog.Descriptor) error {
	if d.Address().ClassID != p.ClassID() {
		return errors.New("unexpected descriptor class")
	}
	p.validated++
	return nil
}
