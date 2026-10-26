package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAggregateOrderedDeclarationIdentity(t *testing.T) {
	original := &OwnedAggregate{
		Definition: AggregateDefinition{Kind: "h", DirectArgs: 1, FinalExtra: true, FinalModify: "w"},
		Helpers: [2]RoutineIdentity{{ID: 901}},
		BinaryRecipe: &AggregateBinaryRecipe{},
		Variadic: true,
		ArgumentNames: []string{"target"},
	}
	copied := *original
	copied.ArgumentNames = append([]string(nil), original.ArgumentNames...)
	if !SameAggregateDefinition(original, &copied) || !SameAggregateDefinition(&copied, original) {
		t.Fatal("independent canonical name storage changed declaration identity")
	}
	if !SameAggregateDefinition(nil, nil) || SameAggregateDefinition(nil, original) || SameAggregateDefinition(original, nil) {
		t.Fatal("nil declaration identity mismatch")
	}
	for name, mutate := range map[string]func(*OwnedAggregate){
		"kind": func(a *OwnedAggregate) { a.Definition.Kind = "o" },
		"direct": func(a *OwnedAggregate) { a.Definition.DirectArgs++ },
		"support": func(a *OwnedAggregate) { a.Definition.Functions[0] = ids.OID(3971) },
		"extra": func(a *OwnedAggregate) { a.Definition.FinalExtra = false },
		"helper": func(a *OwnedAggregate) { a.Helpers[0].ID++ },
		"recipe": func(a *OwnedAggregate) { a.BinaryRecipe = &AggregateBinaryRecipe{} },
		"variadic": func(a *OwnedAggregate) { a.Variadic = false },
		"name": func(a *OwnedAggregate) { a.ArgumentNames[0] = "other" },
		"arity": func(a *OwnedAggregate) { a.ArgumentNames = append(a.ArgumentNames, "row") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *original
			changed.ArgumentNames = append([]string(nil), original.ArgumentNames...)
			mutate(&changed)
			if SameAggregateDefinition(original, &changed) || SameAggregateDefinition(&changed, original) {
				t.Fatal("changed declaration retained identity")
			}
		})
	}
	unnamed := &OwnedAggregate{}
	empty := &OwnedAggregate{ArgumentNames: []string{}}
	if !SameAggregateDefinition(unnamed, empty) {
		t.Fatal("empty name storage changed unnamed declaration identity")
	}
	if original.ArgumentNames[0] != "target" {
		t.Fatal("identity comparisons mutated the source declaration")
	}
}
