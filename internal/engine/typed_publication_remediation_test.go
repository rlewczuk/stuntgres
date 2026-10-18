package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
	root := view.state.descriptors[table.address].(*relationDescriptor)
	backing := view.state.descriptors[source.address].(*relationDescriptor)
	childAddress := view.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.namespace, Name: "child"}]
	child := view.state.descriptors[childAddress].(*relationDescriptor)
	for _, relation := range []*relationDescriptor{backing, root, child} {
		if len(relation.columns) != 2 || relation.columns[0].Name != "id" || relation.columns[1].Name != "label" || relation.columns[0].Usage.Type.ID() != sqltype.Int8 || relation.columns[1].Usage.Type.ID() != sqltype.Text {
			t.Fatal("actual publication lost parent/child shape", relation)
		}
	}
	if !reflect.DeepEqual(backing.attributes, source.attributes) || !reflect.DeepEqual(root.attributes, table.attributes) || !reflect.DeepEqual(child.attributes, []int32{1, 2}) {
		t.Fatal("source-local attribute correspondence changed", backing.attributes, root.attributes, child.attributes)
	}
	if root.ofType != table.ofType || root.rowType != table.rowType || backing.rowType != source.rowType || root.rowType == backing.rowType {
		t.Fatal("source/own-row identity changed")
	}
	if source.columns[0].Usage.Type.ID() != sqltype.Int4 || table.columns[1].Name != "note" {
		t.Fatal("publication mutated captured providers")
	}
	if err := validateTypedAttachment(view, root); err != nil {
		t.Fatal(err)
	}
}

func TestTypedStoredAssociatedCompositeArrayGuard(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	typ := view.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.rowType)}].(*typeDescriptor)
	array := view.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.array)}].(*typeDescriptor)
	holder := inheritanceUnitRelation(e, "holder", 22003, "v")
	holder.columns[0].Usage = array.typ.Usage()
	state := cloneCatalog(view.state)
	state.descriptors[holder.address] = holder
	var diagnostic *sqlstate.Error
	if err := rowRewriteError(e.catalog.read(state), source); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `cannot alter type "pair" because column "holder.v" uses it` {
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
		before, storage := e.CatalogRead().state, e.storage[table.address]
		_, err := e.Execute(op, statement(t, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE"), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `cannot alter table "typed" because column "holder.v" uses its row type` {
			t.Fatal("same-usage semantic action escaped target guard", populated, err)
		}
		if e.CatalogRead().state != before || e.storage[table.address] != storage {
			t.Fatal("rejected same-usage TYPE published", populated)
		}
	}
}
