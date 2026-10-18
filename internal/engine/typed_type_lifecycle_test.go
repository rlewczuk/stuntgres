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
	attached, err := e.newTableDescriptorLocked(table.namespace, "late", table.Owner, append([]ColumnDescriptor(nil), table.Columns...), table.Index)
	if err == nil {
		attached.OfType = table.OfType
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
	if source.Columns[1].Name != "note" || table.Columns[1].Name != "note" {
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
	if imported.OfType.ClassID != ids.TypeClassID || imported.RowType == sqltype.ID(imported.OfType.ObjectID) || !reflect.DeepEqual(imported.Attributes, []int32{1, 7}) {
		t.Fatal("attachment/own-row identity lost", imported)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.RowType)})
	if row.OwningRelation != imported.address || row.SQLType.CompositeMember(1).Attribute != 7 || row.SQLType.CompositeMember(1).Name != "note" {
		t.Fatal("imported own-row codec lost reciprocity", row)
	}
	array, ok := view.LookupArray(row.SQLType)
	if !ok || !array.Concrete() || array.Element() != row.SQLType {
		t.Fatal("imported executable array lost reciprocal generation", array)
	}
	oldRow, _ := before.LookupType(table.RowType)
	oldArray, _ := before.LookupArray(oldRow)
	if !oldArray.Concrete() || array == oldArray || row.SQLType == oldRow {
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
	other.OfType = ids.ObjectAddress{}
	child := *root
	child.address.ObjectID += 2000
	child.OfType = ids.ObjectAddress{}
	child.Attributes = []int32{3, 8}
	child.Inheritance = &tableInheritance{Parents: []ids.ObjectAddress{root.address, other.address}, Columns: map[int32]inheritedColumn{3: {Origins: []inheritanceOrigin{{Parent: root.address, Attribute: 1}, {Parent: other.address, Attribute: 1}}}, 8: {Local: true, Origins: []inheritanceOrigin{{Parent: root.address, Attribute: 7}, {Parent: other.address, Attribute: 7}}}}}
	changed := *root
	changed.Columns, changed.Attributes, changed.Index = root.Columns[1:], root.Attributes[1:], map[string]int{"note": 0}
	state := &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{root.address: &changed, other.address: &other}}
	actions, renames, graph, err := typedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 0 || len(renames) != 0 || !reflect.DeepEqual(graph.Columns[3].Origins, []inheritanceOrigin{{Parent: other.address, Attribute: 1}}) || len(child.Inheritance.Columns[3].Origins) != 2 {
		t.Fatal("surviving other-parent origin lost", actions, renames, graph, err)
	}
	other.Columns, other.Attributes, other.Index = nil, nil, map[string]int{}
	actions, _, graph, err = typedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 1 || actions[0].action.Column.Text != "id" || !graph.Columns[8].Local {
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
				next := *late.base.MustType(table.OfType)
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
			if change == "replacement" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
		})
	}
}
