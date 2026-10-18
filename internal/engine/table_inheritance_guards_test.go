package engine

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// These units exercise graph admission directly, without public SQL or storage.
func TestInheritanceGuards(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	catalog := e.catalog.read(cloneCatalog(base.state))
	parent := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, name: "p", RelationKind: tableRelation, Index: map[string]int{"a": 0}, Attributes: []int32{3}}
	child := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}, name: "c", RelationKind: tableRelation, Index: map[string]int{"a": 0, "local": 1}, Attributes: []int32{5, 8}, NotNullConstraints: []notNullConstraint{{Name: "p_a_not_null", Attribute: 5}}, Inheritance: &tableInheritance{
		parents: []ids.ObjectAddress{parent.address},
		columns: map[int32]inheritedColumn{5: {origins: []inheritanceOrigin{{parent: parent.address, attribute: 3}}}, 8: {local: true}},
		notNull: map[int32]inheritedConstraint{5: {origins: []inheritanceOrigin{{parent: parent.address, attribute: 3, constraint: 90003}}}},
	}}
	standalone := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90004}, RelationKind: tableRelation}
	view := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90005}, RelationKind: viewRelation, View: &viewDefinition{sources: []viewLeaf{{ref: ids.ObjectRef{Address: parent.address}}}}}
	for _, relation := range []*relationDescriptor{parent, child, standalone, view} {
		catalog.state.setDescriptor(relation.address, relation)
	}
	assertError := func(err error, code, message, position string) {
		t.Helper()
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		if !reflect.DeepEqual(err, want) {
			t.Fatalf("diagnostic got %#v want %#v", err, want)
		}
	}
	cyclic := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90006}, RelationKind: viewRelation}
	cyclic.View = &viewDefinition{sources: []viewLeaf{{ref: ids.ObjectRef{Address: cyclic.address}}, {ref: ids.ObjectRef{Address: parent.address}}}}
	catalog.state.setDescriptor(cyclic.address, cyclic)
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
	if child.Inheritance.columns[5].local || len(child.Inheritance.columns[5].origins) != 1 || child.NotNullConstraints[0].Name != "p_a_not_null" {
		t.Fatal("admission changed immutable provenance")
	}
}
