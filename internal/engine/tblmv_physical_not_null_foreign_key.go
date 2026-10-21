package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func PhysicalSelfForeignKeyDeferred(table *catalog.Relation, foreign catalog.ForeignKeyConstraint) bool {
	if len(table.Constraints.PhysicalNotNull) == 0 || foreign.NotValid || foreign.Target != table.Address() {
		return false
	}
	for i := range table.Constraints.Unique {
		primary := &table.Constraints.Unique[i]
		if primary.Primary && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) {
			return true
		}
	}
	return false
}

func EmitPhysicalSelfForeignKeys(view catalog.Read, table *catalog.Relation, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	if len(table.Constraints.PhysicalNotNull) == 0 || len(table.Constraints.ForeignKeys) == 0 {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	for _, foreign := range table.Constraints.ForeignKeys {
		if !PhysicalSelfForeignKeyDeferred(table, foreign) {
			continue
		}
		idx := view.MustRelation(foreign.Index)
		if index.PartitionReplaySeparateConstraint(view, idx) != separate {
			continue
		}
		primary := catalog.PhysicalPrimaryForAttribute(table, foreign.Referenced[0])
		text, err := (stored.Deparser{View: view}).ForeignKeyConstraintSQLWithPrimary(table, foreign, slices.Equal(foreign.Referenced, primary.Attributes))
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}
