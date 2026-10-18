package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTypedPublicationLateAttachmentConflict(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, table)
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	late := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(late)
	e.mu.Lock()
	attached, err := e.newTableDescriptorLocked(table.namespace, "late", table.owner, append([]ColumnDescriptor(nil), table.columns...), table.index)
	if err == nil {
		attached.ofType = table.ofType
		err = e.stageRowTypeLocked(late, attached)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := late.Create(attached); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, late)
	before := e.CatalogRead()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 || e.CatalogRead().state != before.state {
		t.Fatal("late attachment was missed", changes, err)
	}
	if source.columns[1].Name != "note" || table.columns[1].Name != "note" {
		t.Fatal("failed source alteration leaked")
	}
}

func TestTypedPublicationStorageGenerationConflict(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, table)
	defer e.DiscardCatalogStage(stage)
	e.mu.Lock()
	store := e.storage[table.address]
	e.mu.Unlock()
	if store == nil {
		t.Fatal("unit fixture missing physical table")
	}
	stage.rowShapes = map[ids.ObjectAddress]catalogRowShape{table.address: {original: store, replacement: &relationStorage{generation: store.generation + 1}, observedLength: len(store.versions), observedEnds: store.ends, generation: store.generation}}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	concurrent := *store
	concurrent.generation++
	e.storage[table.address] = &concurrent
	e.mu.Unlock()
	before := e.CatalogRead()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 || e.CatalogRead().state != before.state || e.storage[table.address] != &concurrent {
		t.Fatal("storage conflict was not atomic", changes, err)
	}
}

func TestTypedRemapOwnsAttachmentAndReciprocalCodecs(t *testing.T) {
	source, original, table := typedUnitCatalog(t, false)
	before := source.CatalogRead()
	destination := New()
	if _, err := destination.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	view := destination.CatalogRead()
	imported := view.MustRelation(table.address)
	if err := validateTypedAttachment(view, imported); err != nil {
		t.Fatal(err)
	}
	if imported.ofType.ClassID != ids.TypeClassID || imported.rowType == sqltype.ID(imported.ofType.ObjectID) || !reflect.DeepEqual(imported.attributes, []int32{1, 7}) {
		t.Fatal("attachment/own-row identity lost", imported)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.rowType)})
	if row.owningRelation != imported.address || row.typ.CompositeMember(1).Attribute != 7 || row.typ.CompositeMember(1).Name != "note" {
		t.Fatal("imported own-row codec lost reciprocity", row)
	}
	array, ok := view.LookupArray(row.typ)
	if !ok || !array.Concrete() || array.Element() != row.typ {
		t.Fatal("imported executable array lost reciprocal generation", array)
	}
	oldRow, _ := before.LookupType(table.rowType)
	oldArray, _ := before.LookupArray(oldRow)
	if !oldArray.Concrete() || array == oldArray || row.typ == oldRow {
		t.Fatal("imported composite/array borrowed foreign codec")
	}
	if source.CatalogRead().state != before.state || before.Get(original.address) != original || before.Get(table.address) != table {
		t.Fatal("foreign import mutated source capture")
	}
}

func TestTypedInheritanceStableDropOriginRetention(t *testing.T) {
	e, _, root := typedUnitCatalog(t, false)
	other := *root
	other.address.ObjectID += 1000
	other.ofType = ids.ObjectAddress{}
	child := *root
	child.address.ObjectID += 2000
	child.ofType = ids.ObjectAddress{}
	child.attributes = []int32{3, 8}
	child.inheritance = &tableInheritance{parents: []ids.ObjectAddress{root.address, other.address}, columns: map[int32]inheritedColumn{3: {origins: []inheritanceOrigin{{parent: root.address, attribute: 1}, {parent: other.address, attribute: 1}}}, 8: {local: true, origins: []inheritanceOrigin{{parent: root.address, attribute: 7}, {parent: other.address, attribute: 7}}}}}
	changed := *root
	changed.columns, changed.attributes, changed.index = root.columns[1:], root.attributes[1:], map[string]int{"note": 0}
	state := &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{root.address: &changed, other.address: &other}}
	actions, renames, graph, err := typedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 0 || len(renames) != 0 || !reflect.DeepEqual(graph.columns[3].origins, []inheritanceOrigin{{parent: other.address, attribute: 1}}) || len(child.inheritance.columns[3].origins) != 2 {
		t.Fatal("surviving other-parent origin lost", actions, renames, graph, err)
	}
	other.columns, other.attributes, other.index = nil, nil, map[string]int{}
	actions, _, graph, err = typedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 1 || actions[0].action.Column.Text != "id" || !graph.columns[8].local {
		t.Fatal("local column or retired origin lost", actions, graph, err)
	}
}

func TestTypedPublicationFinalAuthorizationFence(t *testing.T) {
	for _, change := range []string{"session", "roles"} {
		t.Run(change, func(t *testing.T) {
			e, source, table := typedUnitCatalog(t, false)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := op.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			stage := typedRenameStage(t, e, source, table)
			defer e.DiscardCatalogStage(stage)
			stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roleRecord.revision
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if change == "session" {
				op.session.mu.Lock()
				next := *op.session.authorization
				next.revision++
				op.session.authorization = &next
				op.session.mu.Unlock()
			} else {
				e.mu.Lock()
				next := *e.roles
				next.revision++
				e.roles = &next
				e.mu.Unlock()
			}
			before := e.CatalogRead()
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
			if err == nil || err.Error() != "stuntgres: authorization changed before publication" || len(changes.Definitions) != 0 || e.CatalogRead().state != before.state {
				t.Fatal("stale authority published typed closure", changes, err)
			}
		})
	}
}

func TestTypedPublicationSourceRevisionAndReplacementFence(t *testing.T) {
	for _, change := range []string{"source", "replacement"} {
		t.Run(change, func(t *testing.T) {
			e, source, table := typedUnitCatalog(t, false)
			stage := typedRenameStage(t, e, source, table)
			defer e.DiscardCatalogStage(stage)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if change == "replacement" {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				late := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(late)
				next := *late.base.MustType(table.ofType)
				next.revision++
				if err := late.Replace(&next); err != nil {
					t.Fatal(err)
				}
				domainUnitPublish(t, e, late)
			}
			before := e.CatalogRead()
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if err == nil || len(changes.Definitions) != 0 || e.CatalogRead().state != before.state {
				t.Fatal("stale source/replacement publication was not atomic", changes, err)
			}
			if change == "replacement" && !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal(err)
			}
		})
	}
}
