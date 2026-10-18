package engine

import (
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// These units exercise graph admission directly, without public SQL or storage.
func TestInheritanceGuards(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	catalog := e.catalog.read(catalog.CloneState(base.State()))
	parent := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, ObjectName: "p", RelationKind: tableRelation, Index: map[string]int{"a": 0}, Attributes: []int32{3}}
	child := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, ObjectName: "c", RelationKind: tableRelation, Index: map[string]int{"a": 0, "local": 1}, Attributes: []int32{5, 8}, NotNullConstraints: []notNullConstraint{{Name: "p_a_not_null", Attribute: 5}}, Inheritance: &tableInheritance{
		Parents: []ids.ObjectAddress{parent.Address()},
		Columns: map[int32]inheritedColumn{5: {Origins: []inheritanceOrigin{{Parent: parent.Address(), Attribute: 3}}}, 8: {Local: true}},
		NotNull: map[int32]inheritedConstraint{5: {Origins: []inheritanceOrigin{{Parent: parent.Address(), Attribute: 3, Constraint: 90003}}}},
	}}
	standalone := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90004}, RelationKind: tableRelation}
	view := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90005}, RelationKind: viewRelation, View: &viewDefinition{sources: []viewLeaf{{ref: ids.ObjectRef{Address: parent.Address()}}}}}
	for _, relation := range []*relationDescriptor{parent, child, standalone, view} {
		catalog.State().SetDescriptor(relation.Address(), relation)
	}
	assertError := func(err error, code, message, position string) {
		t.Helper()
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		if !reflect.DeepEqual(err, want) {
			t.Fatalf("diagnostic got %#v want %#v", err, want)
		}
	}
	cyclic := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90006}, RelationKind: viewRelation}
	cyclic.View = &viewDefinition{sources: []viewLeaf{{ref: ids.ObjectRef{Address: cyclic.Address()}}, {ref: ids.ObjectRef{Address: parent.Address()}}}}
	catalog.State().SetDescriptor(cyclic.Address(), cyclic)
	assertError(inheritanceTargetGuard(catalog, cyclic), "0A000", "recursive table inheritance is unsupported", "")
	for _, relation := range []*relationDescriptor{parent, view} {
		assertError(inheritanceTargetGuard(catalog, relation), "0A000", "recursive table inheritance is unsupported", "")
	}
	for _, relation := range []*relationDescriptor{child, standalone} {
		if err := inheritanceTargetGuard(catalog, relation); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []*relationDescriptor{parent, child, standalone} {
		if err := inheritanceAlterActionsGuard(catalog, relation, []parser.AlterTableAction{{Action: parser.SetNotNull, Column: parser.Identifier{Text: "a"}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range []string{"rename", "drop", "alter"} {
		position := ""
		if operation == "alter" {
			position = "21"
		}
		assertError(inheritanceColumnGuard(child, "a", operation, parser.Position{Character: 21}), "42P16", "cannot "+operation+` inherited column "a"`, position)
		if err := inheritanceColumnGuard(child, "local", operation, parser.Position{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []parser.AlterTableAction{
		{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "p_a_not_null"}},
		{Action: parser.DropNotNull, Column: parser.Identifier{Text: "a"}},
	} {
		assertError(inheritanceAlterActionsGuard(catalog, child, []parser.AlterTableAction{{Action: parser.AddColumn}, action}), "42P16", `cannot drop inherited constraint "p_a_not_null" of relation "c"`, "")
	}
	if err := inheritanceAlterActionsGuard(catalog, child, []parser.AlterTableAction{{Action: parser.DropColumn, Column: parser.Identifier{Text: "local"}}}); err != nil {
		t.Fatal(err)
	}
	if child.Inheritance.Columns[5].Local || len(child.Inheritance.Columns[5].Origins) != 1 || child.NotNullConstraints[0].Name != "p_a_not_null" {
		t.Fatal("admission changed immutable provenance")
	}
}
