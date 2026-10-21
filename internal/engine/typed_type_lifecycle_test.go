package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestTypedPublicationLateAttachmentConflict(t *testing.T) {
	e, source, tbl := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, tbl)
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	late := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(late)
	e.mu.Lock()
	attached, err := table.NewRelation(ddlHost{e: e}, tbl.Namespace(), "late", tbl.Owner, append([]catalog.Column(nil), tbl.Columns...), tbl.Index)
	if err == nil {
		attached.OfType = tbl.OfType
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
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() {
		t.Fatal("late attachment was missed", changes, err)
	}
	if source.Columns[1].Name != "note" || tbl.Columns[1].Name != "note" {
		t.Fatal("failed source alteration leaked")
	}
}

func TestTypedPublicationStorageGenerationConflict(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	stage := typedRenameStage(t, e, source, table)
	defer e.DiscardCatalogStage(stage)
	e.mu.Lock()
	store := e.storage.Relations[table.Address()]
	e.mu.Unlock()
	if store == nil {
		t.Fatal("unit fixture missing physical table")
	}
	stage.rowShapes = map[ids.ObjectAddress]catalogRowShape{table.Address(): {original: store, replacement: &storage.Relation{Generation: store.Generation + 1}, observedLength: len(store.Versions), observedEnds: store.Ends, generation: store.Generation}}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	concurrent := *store
	concurrent.Generation++
	e.storage.InstallRelationLocked(table.Address(), &concurrent)
	e.mu.Unlock()
	before := e.CatalogRead()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() || e.storage.Relations[table.Address()] != &concurrent {
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
	imported := view.MustRelation(table.Address())
	if err := typedef.ValidateTypedAttachment(view, imported); err != nil {
		t.Fatal(err)
	}
	if imported.OfType.ClassID != ids.TypeClassID || imported.RowType == sqltype.ID(imported.OfType.ObjectID) || !reflect.DeepEqual(imported.Attributes, []int32{1, 7}) {
		t.Fatal("attachment/own-row identity lost", imported)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.RowType)})
	if row.OwningRelation != imported.Address() || row.SQLType.CompositeMember(1).Attribute != 7 || row.SQLType.CompositeMember(1).Name != "note" {
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
	if source.CatalogRead().State() != before.State() || before.Get(original.Address()) != original || before.Get(table.Address()) != table {
		t.Fatal("foreign import mutated source capture")
	}
}

func TestTypedInheritanceStableDropOriginRetention(t *testing.T) {
	e, _, root := typedUnitCatalog(t, false)
	other := *root
	other.ObjectAddress.ObjectID += 1000
	other.OfType = ids.ObjectAddress{}
	child := *root
	child.ObjectAddress.ObjectID += 2000
	child.OfType = ids.ObjectAddress{}
	child.Attributes = []int32{3, 8}
	child.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{root.Address(), other.Address()}, Columns: map[int32]catalog.InheritedColumn{3: {Origins: []catalog.InheritanceOrigin{{Parent: root.Address(), Attribute: 1}, {Parent: other.Address(), Attribute: 1}}}, 8: {Local: true, Origins: []catalog.InheritanceOrigin{{Parent: root.Address(), Attribute: 7}, {Parent: other.Address(), Attribute: 7}}}}}
	changed := *root
	changed.Columns, changed.Attributes, changed.Index = root.Columns[1:], root.Attributes[1:], map[string]int{"note": 0}
	state := catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{root.Address(): &changed, other.Address(): &other}, nil)
	actions, renames, graph, err := table.TypedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 0 || len(renames) != 0 || !reflect.DeepEqual(graph.Columns[3].Origins, []catalog.InheritanceOrigin{{Parent: other.Address(), Attribute: 1}}) || len(child.Inheritance.Columns[3].Origins) != 2 {
		t.Fatal("surviving other-parent origin lost", actions, renames, graph, err)
	}
	other.Columns, other.Attributes, other.Index = nil, nil, map[string]int{}
	actions, _, graph, err = table.TypedInheritedActions(e.catalog.read(state), &child, nil)
	if err != nil || len(actions) != 1 || actions[0].Action.Column.Text != "id" || !graph.Columns[8].Local {
		t.Fatal("local column or retired origin lost", actions, graph, err)
	}
}

func TestTypedPublicationFinalAuthorizationFence(t *testing.T) {
	for _, change := range []string{"session", "roles"} {
		t.Run(change, func(t *testing.T) {
			e, source, table := typedUnitCatalog(t, false)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			auth, err := operationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			stage := typedRenameStage(t, e, source, table)
			defer e.DiscardCatalogStage(stage)
			stage.authSession, stage.authRevision, stage.roleRevision = op.Session, auth.Revision, auth.RoleRecord.Revision
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if change == "session" {
				op.Session.Mu.Lock()
				next := *op.Session.Authorization
				next.Revision++
				op.Session.Authorization = &next
				op.Session.Mu.Unlock()
			} else {
				e.mu.Lock()
				next := *e.roles.published
				next.Revision++
				e.roles.published = &next
				e.mu.Unlock()
			}
			before := e.CatalogRead()
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
			if err == nil || err.Error() != "stuntgres: authorization changed before publication" || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() {
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
				next := *late.Base.MustType(table.OfType)
				next.ObjectRevision++
				if err := late.Replace(&next); err != nil {
					t.Fatal(err)
				}
				domainUnitPublish(t, e, late)
			}
			before := e.CatalogRead()
			changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil || len(changes.Definitions) != 0 || e.CatalogRead().State() != before.State() {
				t.Fatal("stale source/replacement publication was not atomic", changes, err)
			}
			if change == "replacement" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
		})
	}
}
