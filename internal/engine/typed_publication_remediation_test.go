package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTypedInheritedParentAttributePublication(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE child() INHERITS(typed)")
	roleExecute(t, e, op, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE")
	roleExecute(t, e, op, "ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE")
	roleExecute(t, e, op, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE")
	roleExecute(t, e, op, "ALTER TYPE pair DROP ATTRIBUTE flag CASCADE")
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
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE holder(v typed)")
	for _, populated := range []bool{false, true} {
		if populated {
			roleExecute(t, e, op, "INSERT INTO holder VALUES('(11,kept)')")
		}
		before, storage := e.CatalogRead().State(), e.Storage.Relations[table.Address()]
		_, err := e.Execute(op, statement(t, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE"), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `cannot alter table "typed" because column "holder.v" uses its row type` {
			t.Fatal("same-usage semantic action escaped target guard", populated, err)
		}
		if e.CatalogRead().State() != before || e.Storage.Relations[table.Address()] != storage {
			t.Fatal("rejected same-usage TYPE published", populated)
		}
	}
}
