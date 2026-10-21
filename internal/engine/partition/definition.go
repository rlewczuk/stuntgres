package partition

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Definition is one pending partition edge: the parent and the child being
// created or attached, the child's prepared bound and its raw syntax. The
// engine's pending definition embeds it and keeps the catalog stage, the
// authorization and the captured data view. DefaultLeaves are the default
// partition's physical leaves whose rows CREATE checks against the new
// bound. Unsupported marks a declared implementation boundary that only
// wins after the key, bound and inherited-definition diagnostics.
type Definition struct {
	Parent, Child *catalog.Relation
	Bound         *catalog.PartitionMembership
	Raw           parser.PartitionBound
	DefaultLeaves []ids.ObjectRef
	Unsupported   bool
}

// CheckSupported returns the 0A000 of an unsupported combination.
func CheckSupported(d *Definition) error {
	if d.Unsupported {
		return sqlError("0A000", "partitioned table constraint combination is unsupported")
	}
	return nil
}

// WithChild returns a copy of parent with child added to its partitions, as
// the default partition when defaultBound is set, and its revision bumped.
func WithChild(parent *catalog.Relation, child ids.ObjectAddress, defaultBound bool) (*catalog.Relation, error) {
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	definition := *parent.Partition
	definition.Children = append(slices.Clone(definition.Children), child)
	if defaultBound {
		definition.DefaultChild = child
	}
	next.Partition = &definition
	next.ObjectRevision++
	return &next, nil
}

// WithoutChild returns a copy of parent with child removed from its
// partitions (and as its default partition) and its revision bumped.
func WithoutChild(parent *catalog.Relation, child ids.ObjectAddress) (*catalog.Relation, error) {
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	definition := *parent.Partition
	definition.Children = slices.DeleteFunc(slices.Clone(definition.Children), func(a ids.ObjectAddress) bool { return a == child })
	if definition.DefaultChild == child {
		definition.DefaultChild = ids.ObjectAddress{}
	}
	next.Partition = &definition
	next.ObjectRevision++
	return &next, nil
}

// CheckDefaultConflict rejects a second default partition named name of
// parent; pos is the bound's position. view reads the existing default.
func CheckDefaultConflict(view catalog.Read, parent *catalog.Relation, bound *catalog.PartitionMembership, name string, pos parser.Position) error {
	if !bound.DefaultBound || parent.Partition.DefaultChild == (ids.ObjectAddress{}) {
		return nil
	}
	existing := view.MustRelation(parent.Partition.DefaultChild)
	return expr.ErrorAt("42P17", fmt.Sprintf(`partition "%s" conflicts with existing default partition "%s"`, name, existing.Name()), pos)
}
