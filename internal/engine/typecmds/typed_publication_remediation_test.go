package typecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTypedInheritedParentAttributePublication(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE child() INHERITS(typed)")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE")
	enginetest.RoleExecute(t, e, op, "ALTER TYPE pair DROP ATTRIBUTE flag CASCADE")
	view := e.CatalogRead()
	root := view.MustRelation(table.Address())
	backing := view.MustRelation(source.Address())
	childAddress := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: "child"})
	child := view.MustRelation(childAddress)
	for _, relation := range []*catalog.Relation{backing, root, child} {
		if len(relation.Columns) != 2 || relation.Columns[0].Name != "id" || relation.Columns[1].Name != "label" || relation.Columns[0].Usage.Type.ID() != sqltype.Int8 || relation.Columns[1].Usage.Type.ID() != sqltype.Text {
			t.Fatal("actual publication lost parent/child shape", relation)
		}
	}
	if !reflect.DeepEqual(backing.Attributes, source.Attributes) || !reflect.DeepEqual(root.Attributes, table.Attributes) || !reflect.DeepEqual(child.Attributes, []int32{1, 2}) {
		t.Fatal("source-local attribute correspondence changed", backing.Attributes, root.Attributes, child.Attributes)
	}
	if root.OfType != table.OfType || root.RowType != table.RowType || backing.RowType != source.RowType || root.RowType == backing.RowType {
		t.Fatal("source/own-row identity changed")
	}
	if source.Columns[0].Usage.Type.ID() != sqltype.Int4 || table.Columns[1].Name != "note" {
		t.Fatal("publication mutated captured providers")
	}
	if err := typedef.ValidateTypedAttachment(view, root); err != nil {
		t.Fatal(err)
	}
}

func TestTypedStoredAssociatedCompositeArrayGuard(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
	holder := inheritanceUnitRelation(e, "holder", 22003, "v")
	holder.Columns[0].Usage = array.SQLType.Usage()
	state := catalog.CloneState(view.State())
	state.SetDescriptor(holder.Address(), holder)
	var diagnostic *sqlstate.Error
	if err := typedef.RowRewriteError(e.Catalog.Read(state), source); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `cannot alter type "pair" because column "holder.v" uses it` {
		t.Fatal("real associated-array provider escaped guard", err)
	}
}

func TestTypedSameUsageStoredOwnRowPublicationGuard(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE holder(v typed)")
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.RoleExecute(t, e, op, "INSERT INTO holder VALUES('(11,kept)')")
		}
		before, storage := e.CatalogRead().State(), e.Storage.Relations[table.Address()]
		_, err := e.Execute(op, enginetest.Statement(t, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE"), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `cannot alter table "typed" because column "holder.v" uses its row type` {
			t.Fatal("same-usage semantic action escaped target guard", populated, err)
		}
		if e.CatalogRead().State() != before || e.Storage.Relations[table.Address()] != storage {
			t.Fatal("rejected same-usage TYPE published", populated)
		}
	}
}

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []catalog.Column
	if !empty {
		columns = []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: enginetest.Usage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.Mu.Lock()
	source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "pair", auth.State.CurrentUser, append([]catalog.Column(nil), columns...), index)
	if err == nil {
		source.RelationKind = catalog.CompositeRelation
		if !empty {
			source.Attributes = []int32{2, 4}
			source.ColumnMeta.HighWater = 4
			source.Columns[0].OriginAttribute = 2
			source.Columns[1].OriginAttribute = 4
		}
		err = e.StageRowTypeLocked(stage, source)
	}
	var tbl *catalog.Relation
	if err == nil {
		tbl, err = table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "typed", source.Owner, append([]catalog.Column(nil), columns...), index)
	}
	if err == nil {
		tbl.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
		if !empty {
			tbl.Attributes = []int32{1, 7}
			tbl.ColumnMeta.HighWater = 7
			tbl.Columns[1].OriginAttribute = 7
		}
		err = e.StageRowTypeLocked(stage, tbl)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(tbl); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.MustRelation(source.Address()), view.MustRelation(tbl.Address())
}

func inheritanceUnitRelation(e *core.Engine, name string, id ids.OID, names ...string) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Index: make(map[string]int)}
	for i, name := range names {
		d.Index[name] = i
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
