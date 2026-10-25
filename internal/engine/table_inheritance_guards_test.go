package engine

import (
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// These units exercise graph admission directly, without public SQL or storage.
func TestInheritanceGuards(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	catalogView := e.Catalog.Read(catalog.CloneState(base.State()))
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, ObjectName: "p", RelationKind: catalog.TableRelation, Index: map[string]int{"a": 0}, Attributes: []int32{3}}
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, ObjectName: "c", RelationKind: catalog.TableRelation, Index: map[string]int{"a": 0, "local": 1}, Attributes: []int32{5, 8}, Constraints: catalog.RelationConstraints{NotNull: []catalog.NotNullConstraint{{Name: "p_a_not_null", Attribute: 5}}}, Inheritance: &catalog.TableInheritance{
		Parents: inheritanceUnitParents(parent.Address()),
		Columns: map[int32]catalog.InheritedColumn{5: {Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 3}}}, 8: {Local: true}},
		NotNull: map[int32]catalog.InheritedConstraint{5: {Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 3, Constraint: 90003}}}},
	}}
	standalone := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90004}, RelationKind: catalog.TableRelation}
	view := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90005}, RelationKind: catalog.ViewRelation, View: stored.NewView(stored.ViewParts{Sources: []stored.ViewLeaf{{Ref: ids.ObjectRef{Address: parent.Address()}}}})}
	for _, relation := range []*catalog.Relation{parent, child, standalone, view} {
		catalogView.State().SetDescriptor(relation.Address(), relation)
	}
	assertError := func(err error, code, message, position string) {
		t.Helper()
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		if !reflect.DeepEqual(err, want) {
			t.Fatalf("diagnostic got %#v want %#v", err, want)
		}
	}
	cyclic := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90006}, RelationKind: catalog.ViewRelation}
	cyclic.View = stored.NewView(stored.ViewParts{Sources: []stored.ViewLeaf{{Ref: ids.ObjectRef{Address: cyclic.Address()}}, {Ref: ids.ObjectRef{Address: parent.Address()}}}})
	catalogView.State().SetDescriptor(cyclic.Address(), cyclic)
	assertError(table.InheritanceTargetGuard(catalogView, cyclic), "0A000", "recursive table inheritance is unsupported", "")
	for _, relation := range []*catalog.Relation{parent, view} {
		assertError(table.InheritanceTargetGuard(catalogView, relation), "0A000", "recursive table inheritance is unsupported", "")
	}
	for _, relation := range []*catalog.Relation{child, standalone} {
		if err := table.InheritanceTargetGuard(catalogView, relation); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []*catalog.Relation{parent, child, standalone} {
		if err := table.InheritanceAlterActionsGuard(catalogView, relation, []parser.AlterTableAction{{Action: parser.SetNotNull, Column: parser.Identifier{Text: "a"}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range []string{"rename", "drop", "alter"} {
		position := ""
		if operation == "alter" {
			position = "21"
		}
		assertError(table.InheritanceColumnGuard(child, "a", operation, parser.Position{Character: 21}), "42P16", "cannot "+operation+` inherited column "a"`, position)
		if err := table.InheritanceColumnGuard(child, "local", operation, parser.Position{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []parser.AlterTableAction{
		{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "p_a_not_null"}},
		{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}},
	} {
		assertError(table.InheritanceAlterActionsGuard(catalogView, child, []parser.AlterTableAction{{Action: parser.AddColumn}, action}), "42P16", `cannot drop inherited constraint "p_a_not_null" of relation "c"`, "")
	}
	if err := table.InheritanceAlterActionsGuard(catalogView, child, []parser.AlterTableAction{{Action: parser.DropColumn, Column: parser.Identifier{Text: "local"}}}); err != nil {
		t.Fatal(err)
	}
	if child.Inheritance.Columns[5].Local || len(child.Inheritance.Columns[5].Origins) != 1 || child.Constraints.NotNull[0].Name != "p_a_not_null" {
		t.Fatal("admission changed immutable provenance")
	}
}
