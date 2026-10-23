package table

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestCastDefinitionGenerationPlaceholderSeeds(t *testing.T) {
	for _, name := range []string{"none", "child", "isolated", "both"} {
		t.Run(name, func(t *testing.T) {
			f := newPendingFixture()
			var tables []DumpRelation
			for _, descriptor := range []*catalog.Relation{f.c2, f.q, f.c1, f.p} {
				descriptor.Constraints.Check = []catalog.CheckConstraint{{Name: "valid_check"}, {Name: "invalid_check", NotValid: true}}
				tables = append(tables, DumpRelation{Descriptor: descriptor})
			}
			var seeds map[ids.ObjectAddress]bool
			if name != "none" {
				seeds = make(map[ids.ObjectAddress]bool)
				if name == "child" || name == "both" {
					seeds[f.c1.Address()] = true
				}
				if name == "isolated" || name == "both" {
					seeds[f.q.Address()] = true
				}
			}
			PrepareGenerationPlaceholders(f.view, tables, seeds)
			for _, table := range tables {
				component := f.p.Address()
				selected := name == "child" || name == "both"
				if table.Descriptor == f.q {
					component = f.q.Address()
					selected = name == "isolated" || name == "both"
				}
				if table.RecipePlaceholders != selected {
					t.Fatal("explicit seed did not select exactly its existing inheritance component", table.Descriptor.Address(), table.RecipePlaceholders)
				}
				if selected {
					if table.RecipeComponent != component || !table.DeferredChecks["valid_check"] || table.DeferredChecks["invalid_check"] {
						t.Fatal("component identity or CHECK validity changed", table.Descriptor.Address(), table.RecipeComponent, table.DeferredChecks)
					}
				} else if table.RecipeComponent != (ids.ObjectAddress{}) || len(table.DeferredChecks) != 0 {
					t.Fatal("unselected component gained replay state", table.Descriptor.Address())
				}
			}
		})
	}
}
