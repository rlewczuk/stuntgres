package index

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// CoverageReader is what partitioned-index coverage reads.
type CoverageReader interface {
	LiveDescriptor(ids.ObjectAddress) catalog.Descriptor
	Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	Removed(ids.ObjectAddress) bool
}

// catalogCoverage reads coverage from a published or candidate catalog with no
// staged or prepared removals.
type catalogCoverage struct{ view catalog.Read }

func (c catalogCoverage) LiveDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	return c.view.Get(address)
}

func (c catalogCoverage) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return c.view.Descriptors()
}

func (catalogCoverage) Removed(ids.ObjectAddress) bool { return false }

func PartitionIndexCoverageComplete(view catalog.Read, index *catalog.Relation) bool {
	return CoverageComplete(catalogCoverage{view}, index)
}

// CoverageComplete reports whether every partition of the index's parent
// has a valid attached child index.
func CoverageComplete(r CoverageReader, index *catalog.Relation) bool {
	table, ok := r.LiveDescriptor(index.SQLIndex.Parent).(*catalog.Relation)
	if !ok || table.Partition == nil {
		return index.SQLIndex.Valid
	}
	for _, child := range table.Partition.Children {
		found := false
		for address := range r.Descriptors() {
			if r.Removed(address) {
				continue
			}
			candidate, ok := r.LiveDescriptor(address).(*catalog.Relation)
			if ok && candidate.SQLIndex != nil && candidate.SQLIndex.PartitionParent == index.Address() && candidate.SQLIndex.Parent == child && candidate.SQLIndex.Valid {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func PartitionIndexAttachCheck(ctx context.Context, view catalog.Read, parent, child *catalog.Relation) error {
	diagnostic := func(code, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf(`cannot attach index "%s" as a partition of index "%s"`, child.Name(), parent.Name()), Detail: detail}
	}
	table := view.MustRelation(parent.SQLIndex.Parent)
	childTable := view.MustRelation(child.SQLIndex.Parent)
	if table.Partition == nil || childTable.Membership == nil || childTable.Membership.Parent != table.Address() {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is not an index on any partition of table "%s".`, child.Name(), table.Name()))
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.Address() != child.Address() && index.SQLIndex.PartitionParent == parent.Address() && index.SQLIndex.Parent == childTable.Address() {
			return diagnostic("55000", fmt.Sprintf(`Another index is already attached for partition "%s".`, childTable.Name()))
		}
	}
	if child.SQLIndex.PartitionParent == parent.Address() {
		return nil
	}
	if child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		return diagnostic("55000", fmt.Sprintf(`Index "%s" is already attached to another index.`, child.Name()))
	}
	if parent.SQLIndex.IsExclusion() || child.SQLIndex.IsExclusion() {
		return diagnostic("42P17", "The index definitions do not match.")
	}
	mapped, err := MapPartitionIndex(ctx, view, table, childTable, parent.SQLIndex)
	if err != nil {
		return err
	}
	match, err := PartitionIndexEquivalent(ctx, view, childTable, mapped, child.SQLIndex)
	if err != nil {
		return err
	}
	if !match {
		return diagnostic("42P17", "The index definitions do not match.")
	}
	if parent.SQLIndex.Constraint != nil {
		if child.SQLIndex.Constraint == nil || child.SQLIndex.Constraint.Kind != catalog.UniqueBackingKind {
			return diagnostic("42P17", fmt.Sprintf(`The index "%s" belongs to a constraint in table "%s" but no constraint exists for index "%s".`, parent.Name(), table.Name(), child.Name()))
		}
		if parent.SQLIndex.Constraint.Primary {
			for _, key := range child.SQLIndex.Keys {
				if childTable.Columns[childTable.IndexForAttribute(key.Attribute)].Nullable {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: "invalid primary key definition", Detail: fmt.Sprintf(`Column "%s" of relation "%s" is not marked NOT NULL.`, childTable.Columns[childTable.IndexForAttribute(key.Attribute)].Name, childTable.Name())}
				}
			}
		}
	}
	return nil
}

func ValidatePartitionIndex(view catalog.Read, index, table *catalog.Relation) error {
	d := index.SQLIndex
	if table.Partition != nil && d.Valid && !PartitionIndexCoverageComplete(view, index) {
		return fmt.Errorf("stuntgres: inconsistent virtual index validity")
	}
	if err := PartitionUniqueCoverage(view, table, d); err != nil {
		return err
	}
	if d.PartitionParent == (ids.ObjectAddress{}) {
		for _, c := range table.Constraints.Unique {
			if c.Index == index.Address() && c.Parent != 0 {
				return fmt.Errorf("stuntgres: orphan partition constraint ancestry")
			}
		}
		for _, c := range table.Constraints.Exclusion {
			if c.Index == index.Address() && c.Parent != 0 {
				return fmt.Errorf("stuntgres: orphan partition exclusion ancestry")
			}
		}
		return nil
	}
	parent, ok := view.Relation(d.PartitionParent)
	if !ok || parent.SQLIndex == nil || table.Membership == nil || parent.SQLIndex.Parent != table.Membership.Parent {
		return fmt.Errorf("stuntgres: invalid partition index ancestry")
	}
	for _, descriptor := range view.Descriptors() {
		other, ok := descriptor.(*catalog.Relation)
		if ok && other.Address() != index.Address() && other.SQLIndex != nil && other.SQLIndex.PartitionParent == d.PartitionParent && other.SQLIndex.Parent == table.Address() {
			return fmt.Errorf("stuntgres: duplicate partition index ancestry")
		}
	}
	parentTable := view.MustRelation(parent.SQLIndex.Parent)
	mapped, err := MapPartitionIndex(context.Background(), view, parentTable, table, parent.SQLIndex)
	if err != nil {
		return err
	}
	match, err := PartitionIndexEquivalent(context.Background(), view, table, mapped, d)
	if err != nil {
		return err
	}
	if !match {
		return fmt.Errorf("stuntgres: inconsistent partition index definition")
	}
	remaining := view.DescriptorCount()
	for address := d.PartitionParent; address != (ids.ObjectAddress{}); remaining-- {
		if remaining == 0 || address == index.Address() {
			return fmt.Errorf("stuntgres: cyclic partition index ancestry")
		}
		ancestor, ok := view.Relation(address)
		if !ok || ancestor.SQLIndex == nil {
			return fmt.Errorf("stuntgres: orphan partition index")
		}
		address = ancestor.SQLIndex.PartitionParent
	}
	if parent.SQLIndex.Constraint != nil && d.Constraint == nil {
		return fmt.Errorf("stuntgres: missing partition constraint backing")
	}
	if d.Constraint != nil {
		expected := ids.OID(0)
		if parent.SQLIndex.Constraint != nil {
			expected = parent.SQLIndex.Constraint.OID
		}
		found := false
		for _, c := range table.Constraints.Unique {
			if c.Index == index.Address() && c.Parent == expected && c.IsLocal == (expected == 0) {
				found = true
			}
		}
		for _, c := range table.Constraints.Exclusion {
			if c.Index == index.Address() && c.Parent == expected && c.IsLocal == (expected == 0) {
				source := ExclusionDefinition(parentTable, parent)
				found = source != nil && c.Deferrable == source.Deferrable && c.InitiallyDeferred == source.InitiallyDeferred
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: inconsistent partition constraint ancestry")
		}
	}
	return nil
}

// PartitionIndexValidity recomputes the validity of every partitioned index
// in t. Only explicit ATTACH INDEX can promote that index (promote) and its
// ancestors; table edge changes preserve invalidity even when coverage
// becomes complete. Each pass resolves another level; the catalog validator
// rejects cycles.
func PartitionIndexValidity(t ValidityTarget, promote ids.ObjectAddress) error {
	for range t.DescriptorCount() {
		changed := false
		for address := range t.Descriptors() {
			if t.Removed(address) {
				continue
			}
			descriptor := t.LiveDescriptor(address)
			idx, ok := descriptor.(*catalog.Relation)
			if !ok || idx.SQLIndex == nil {
				continue
			}
			table, ok := t.LiveDescriptor(idx.SQLIndex.Parent).(*catalog.Relation)
			if !ok || table.Partition == nil {
				continue
			}
			valid := CoverageComplete(t, idx)
			if valid && !idx.SQLIndex.Valid && !PartitionIndexPromotionPath(t, promote, idx.Address()) {
				continue
			}
			if idx.SQLIndex.Valid == valid {
				continue
			}
			if idx.Revision() == ids.DefinitionRevision(^uint64(0)) {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			next, definition := *idx, *idx.SQLIndex
			definition.Valid = valid
			next.SQLIndex = &definition
			next.ObjectRevision++
			if err := t.ReplacePartitionDescriptor(&next); err != nil {
				return err
			}
			changed = true
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: cyclic partition index hierarchy")
}

// PartitionIndexPromotionPath reports whether target is start or one of its
// partitioned-index ancestors.
func PartitionIndexPromotionPath(t ValidityTarget, start, target ids.ObjectAddress) bool {
	for remaining := t.DescriptorCount(); start != (ids.ObjectAddress{}) && remaining > 0; remaining-- {
		if start == target {
			return true
		}
		idx, ok := t.LiveDescriptor(start).(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			return false
		}
		start = idx.SQLIndex.PartitionParent
	}
	return false
}

// StagePartitionIndexAttach stages ALTER INDEX parent ATTACH PARTITION child:
// the partition link, the inherited backing constraint, then the validity
// pass that may promote parent.
func StagePartitionIndexAttach(h Host, parent, child *catalog.Relation) error {
	view := h.StageRead()
	if err := PartitionIndexAttachCheck(h.Context(), view, parent, child); err != nil {
		return err
	}
	if child.SQLIndex.PartitionParent != parent.Address() {
		next, definition := *child, *child.SQLIndex
		definition.PartitionParent = parent.Address()
		next.SQLIndex = &definition
		next.ObjectRevision++
		if err := h.Stage().Replace(&next); err != nil {
			return err
		}
		if parent.SQLIndex.Constraint != nil {
			table := view.MustRelation(child.SQLIndex.Parent)
			changed := *table
			changed.Constraints.Unique = slices.Clone(table.Constraints.Unique)
			changed.ObjectRevision++
			for i := range changed.Constraints.Unique {
				c := &changed.Constraints.Unique[i]
				if c.Index == child.Address() {
					c.Parent = parent.SQLIndex.Constraint.OID
					c.IsLocal = false
				}
			}
			if err := h.Stage().Replace(&changed); err != nil {
				return err
			}
		}
	}
	return PartitionIndexValidity(h.ValidityTarget(), parent.Address())
}
