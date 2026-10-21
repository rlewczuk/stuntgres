package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func PhysicalPrimaryDeferred(table *catalog.Relation, primary *catalog.UniqueConstraint) bool {
	return primary.Primary && len(table.Constraints.PhysicalNotNull) != 0
}

func EmitPhysicalPrimaryCleanup(view catalog.Read, table *catalog.Relation, primary *catalog.UniqueConstraint, emit func(string) error, setOwner func(string) error) error {
	hasPhysicalKey := false
	for _, attribute := range primary.Attributes {
		if _, present := table.Constraints.PhysicalNotNull[attribute]; present {
			hasPhysicalKey = true
			break
		}
	}
	if !hasPhysicalKey {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	keys := make([]string, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = table.Columns[table.IndexForAttribute(attribute)].Name
	}
	if err := setOwner("postgres"); err != nil {
		return err
	}
	for _, attribute := range primary.Attributes {
		if _, physical := table.Constraints.PhysicalNotNull[attribute]; !physical {
			continue
		}
		column := table.Columns[table.IndexForAttribute(attribute)].Name
		if err := emit(physicalNotNullCleanupSQL(qualified, column, primary.Name, keys) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

// Run at declaration time, before descendants or rows. The constructor creates
// its real NN record; the CTE selects that record by key, never an assumed name.
func (t *dumpRelation) EmitPhysicalNotNull(view catalog.Read, emit func(string) error, setOwner func(string) error) error {
	if len(t.Descriptor.Constraints.PhysicalNotNull) == 0 {
		return nil
	}
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	for _, attribute := range t.Attributes {
		if _, physical := t.Descriptor.Constraints.PhysicalNotNull[attribute]; !physical || catalog.PhysicalPrimaryForAttribute(t.Descriptor, attribute) != nil {
			continue
		}
		column := t.Columns[t.Descriptor.IndexForAttribute(attribute)].Name
		if err := setOwner(t.Owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(column) + " SET NOT NULL;\n"); err != nil {
			return err
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		if err := emit(physicalNotNullCleanupSQL(qualified, column, "", nil) + ";\n"); err != nil {
			return err
		}
	}
	for i := range t.Descriptor.Constraints.Unique {
		primary := &t.Descriptor.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		idx, ok := view.Relation(primary.Index)
		if !ok {
			return fmt.Errorf("stuntgres: missing physical primary dump index")
		}
		if index.PartitionReplaySeparateConstraint(view, idx) {
			continue
		}
		text, err := (stored.Deparser{View: view}).UniqueConstraintSQL(t.Descriptor, primary)
		if err != nil {
			return err
		}
		if err := setOwner(t.Owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(primary.Name) + " " + text + ";\n"); err != nil {
			return err
		}
		if err := EmitPhysicalPrimaryCleanup(view, t.Descriptor, primary, emit, setOwner); err != nil {
			return err
		}
	}
	return nil
}
