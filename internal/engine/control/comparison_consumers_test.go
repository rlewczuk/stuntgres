package control

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
)

func TestComparisonBindingDeclarationSites(t *testing.T) {
	const oid ids.OID = 90000
	declaration := castConsumer{Event: dump.Event{OID: oid}}
	inheritedDefault := castConsumer{Event: declaration.Event, Attribute: 1}
	deferredDefault := castConsumer{Event: dump.Event{OID: oid, Phase: dump.Defaults}, Attribute: 1}
	repairedGeneration := castConsumer{Event: dump.Event{OID: oid, Phase: dump.InheritanceRecipes}, Attribute: 2}
	inline := catalog.InheritedColumn{Local: true, ExplicitDefault: true, ExplicitGeneration: true}
	for _, tc := range []struct {
		name                   string
		inherited              bool
		provenance             catalog.InheritedColumn
		recipes                map[int32]catalog.InheritedColumn
		deferred, placeholders bool
		want                   map[string]castConsumer
	}{
		{"ordinary_inline", false, catalog.InheritedColumn{}, nil, false, false, map[string]castConsumer{"default": declaration, "generation": declaration}},
		{"unchanged_inherited", true, catalog.InheritedColumn{}, nil, false, false, map[string]castConsumer{}},
		{"local_column_inherited_recipes", true, catalog.InheritedColumn{Local: true}, nil, false, false, map[string]castConsumer{}},
		{"explicit_local_recipes", true, inline, nil, false, false, map[string]castConsumer{"default": declaration, "generation": declaration}},
		{"retained_nonlocal_default", true, catalog.InheritedColumn{}, map[int32]catalog.InheritedColumn{1: {ExplicitDefault: true}}, false, false, map[string]castConsumer{"default": inheritedDefault}},
		{"effective_local_recipes", true, catalog.InheritedColumn{Local: true}, map[int32]catalog.InheritedColumn{1: inline, 2: inline}, false, false, map[string]castConsumer{"default": declaration, "generation": declaration}},
		{"deferred_inherited_default", true, catalog.InheritedColumn{}, nil, true, false, map[string]castConsumer{"default": deferredDefault}},
		{"repaired_inherited_generation", true, catalog.InheritedColumn{}, nil, false, true, map[string]castConsumer{"generation": repairedGeneration}},
		{"repaired_local_generation", true, inline, nil, false, true, map[string]castConsumer{"default": declaration, "generation": repairedGeneration}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Distinct expression identities isolate declaration ownership from
			// scalar execution; the public inherited witness exercises real SQL.
			defaultExpr, generationExpr := &plan.LiteralExpr{}, &plan.LiteralExpr{}
			relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}}
			relation.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Expression: defaultExpr}}
			relation.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Expression: generationExpr}}
			if tc.inherited {
				relation.Inheritance = &catalog.TableInheritance{Columns: map[int32]catalog.InheritedColumn{1: tc.provenance, 2: tc.provenance}}
			}
			state := catalog.NewState(1)
			state.SetDescriptor(relation.Address(), relation)
			script := dumpScript{Capture: &Capture{}}
			script.CatalogView = catalog.NewRead(nil, state)
			script.TableIndices = map[ids.OID]int{oid: 0}
			script.Tables = []table.DumpRelation{{Descriptor: relation, Recipes: tc.recipes, RecipePlaceholders: tc.placeholders}}
			if tc.deferred {
				script.DeferDefaults = true
				script.Tables[0].DeferredDefinitions = map[int32]bool{1: true}
			}
			got := map[string]castConsumer{}
			err := script.visitBindingConsumers(func(site castConsumer, owner authorization.PrincipalRef, node expr.Expr) error {
				name := "default"
				if node == generationExpr {
					name = "generation"
				} else if node != defaultExpr {
					t.Fatal("invented declaration expression", node)
				}
				if _, duplicate := got[name]; duplicate || owner != relation.Owner {
					t.Fatal("duplicate expression or changed declaration owner", name, owner)
				}
				got[name] = site
				return nil
			})
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("actual declaration sites=%v want=%v error=%v", got, tc.want, err)
			}
			if relation.Inheritance != nil && (relation.Inheritance.Columns[1].Local != tc.provenance.Local || relation.Inheritance.Columns[1].ExplicitDefault != tc.provenance.ExplicitDefault || relation.Inheritance.Columns[2].ExplicitGeneration != tc.provenance.ExplicitGeneration) {
				t.Fatal("classification changed captured declaration provenance")
			}
		})
	}
}
