package stored

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func (t *ruleRangeTable) inherit(ctx context.Context, parent *ruleRangeTable) error {
	if parent == nil {
		return nil
	}
	t.pseudos = parent.pseudos
	for name := range parent.used {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.used == nil {
			t.used = make(map[string]int)
		}
		t.used[name] = 0
	}
	return nil
}

// Rule parsing installs OLD/NEW before ordinary action RTEs. INSERT SELECT
// moves them into its source query; its target and *SELECT* RTE instead form a
// distinct parent namespace, also used by the INSERT's modifying CTE bodies.
func (v *QueryRenderer) initialRuleRangeTable(ctx context.Context, read catalog.Read, frame uint32) (ruleRangeTable, error) {
	var table ruleRangeTable
	if err := ctx.Err(); err != nil {
		return table, err
	}
	if err := table.inherit(ctx, v.ruleParentNames); err != nil {
		return table, err
	}
	pseudos := func(t *ruleRangeTable) {
		if v.ruleParentNames == nil {
			t.pseudos[0] = t.allocate(ruleRenderAlias{frame, 0, -2}, "old", nil).name
			t.pseudos[1] = t.allocate(ruleRenderAlias{frame, 0, -1}, "new", nil).name
		}
	}
	target := func(t *ruleRangeTable) error {
		if v.ruleTarget.Address.ObjectID == 0 {
			return nil
		}
		name := v.ruleTargetAlias
		if name == "" {
			relation, ok := read.Relation(v.ruleTarget.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			name = relation.Name()
		}
		t.target = t.allocate(ruleRenderAlias{frame, 0, 0}, name, nil).name
		return nil
	}
	excluded := func(t *ruleRangeTable) {
		if v.ruleExcluded {
			t.excluded = t.allocate(ruleRenderAlias{frame, 0, 3}, "excluded", nil).name
		}
	}
	if v.ruleInsertSource {
		outer := table
		if err := target(&outer); err != nil {
			return table, err
		}
		outer.allocate(ruleRenderAlias{frame, 0, 1}, "*SELECT*", nil)
		excluded(&outer)
		var source ruleRangeTable
		if err := source.inherit(ctx, &outer); err != nil {
			return source, err
		}
		source.target, source.excluded, source.modifier = outer.target, outer.excluded, &outer
		pseudos(&source)
		if v.ruleValues {
			source.allocate(ruleRenderAlias{frame, 0, 2}, "*VALUES*", nil)
		}
		return source, nil
	}
	pseudos(&table)
	if err := target(&table); err != nil {
		return table, err
	}
	if v.ruleValues {
		table.allocate(ruleRenderAlias{frame, 0, 2}, "*VALUES*", nil)
	}
	excluded(&table)
	return table, nil
}

// The returned maps are finalized before descendants render and remain
// immutable. A modifier's separate View can inherit these facts even though its
// own synthetic frame numbering starts at one again.
func (v *QueryRenderer) ruleNameFacts(outer bool) *ruleRangeTable {
	table := v.ruleRootNames
	if len(v.ruleScopes) != 0 && v.View != nil {
		frame := v.ruleScopes[len(v.ruleScopes)-1]
		if frame != v.View.query.Root.(*plan.ProjectNode).Frame {
			current := v.ruleTables[frame]
			table = &current
		}
	}
	if outer && table != nil && table.modifier != nil {
		return table.modifier
	}
	return table
}

func (t *ruleRangeTable) pseudoName(old bool) string {
	index, fallback := 1, "new"
	if old {
		index, fallback = 0, "old"
	}
	if t != nil && t.pseudos[index] != "" {
		return t.pseudos[index]
	}
	return fallback
}
