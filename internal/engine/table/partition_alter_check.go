package table

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// The root binder is the sole authority for an inherited expression. Transfer
// cooked row identities rather than resolving its source names under each child.
func preparePartitionChecks(h AlterHost, plan *InheritanceAlterPlan, auth ddl.Authority, child *catalog.Relation, raw []*parser.CheckConstraint) (PreparedAddedChecks, error) {
	result := PreparedAddedChecks{Old: child, Raw: raw}
	next := *child
	next.Constraints.Check = slices.Clone(child.Constraints.Check)
	view := h.AccessView(auth).Catalog
	parent := plan.DefinitionRelation(view, child.Membership.Parent)
	start := 0
	for _, c := range child.Constraints.Check {
		if c.Ordinal >= start {
			start = c.Ordinal + 1
		}
	}
	result.Ordinal = start
	slots := make(map[int32]int, len(child.Attributes))
	for i, a := range child.Attributes {
		slots[a] = i
	}
	for offset, declaration := range raw {
		sourceIndex := slices.IndexFunc(parent.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == declaration.Name.Text })
		if sourceIndex < 0 {
			return result, ids.ErrCatalogReplaced
		}
		source := parent.Constraints.Check[sourceIndex]
		rowRef := func(ref ids.ObjectRef) ids.ObjectRef {
			if ref.Address.ClassID == parent.Address().ClassID && ref.Address.ObjectID == parent.Address().ObjectID {
				attribute := ref.Address.SubID
				ref.Address = child.Address()
				if attribute != 0 {
					ref.Address.SubID = child.Attributes[child.Index[parent.Columns[parent.IndexForAttribute(attribute)].Name]]
				}
			}
			return ref
		}
		expression, err := stored.RemapExpressionWithQuery(source.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots, nil, rowRef)
		if err != nil {
			return result, err
		}
		existing := slices.IndexFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == source.Name })
		if existing >= 0 {
			old := next.Constraints.Check[existing]
			if old.NoInherit {
				return result, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on relation "%s"`, old.Name, child.Name()))
			}
			equality := analyze.Binder{Consumer: analyze.ConsumerContext{CheckDefinitionEquality: true}}
			if !equality.SameGroupExpression(expression, old.Expression) {
				return result, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, old.Name, child.Name()))
			}
			if old.NotValid && !source.NotValid {
				return result, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on relation "%s"`, old.Name, child.Name()))
			}
			ddl.Notice(h, "00000", fmt.Sprintf(`merging constraint "%s" with inherited definition`, old.Name))
			continue // No new event: this action's contribution stops at this merge.
		}
		if catalog.RelationConstraintNameExists(&next, source.Name) {
			return result, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, source.Name, child.Name()))
		}
		c := source
		c.OID = 0
		c.Ordinal = start + offset
		c.Expression = expression
		c.PartitionOrigin = &catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Constraint: source.OID}}}
		c.Dependencies = slices.Clone(source.Dependencies)
		for i := range c.Dependencies {
			c.Dependencies[i].Ref = rowRef(c.Dependencies[i].Ref)
			if c.Dependencies[i].Ref.Address.ObjectID == child.Address().ObjectID && c.Dependencies[i].Ref.Address.ClassID == ids.RelationClassID {
				c.Dependencies[i].Revision = child.Revision()
			}
		}
		next.Constraints.Check = append(next.Constraints.Check, c)
		if !c.NotValid {
			result.Scans = append(result.Scans, c)
		}
	}
	slices.SortFunc(next.Constraints.Check, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	result.Next = &next
	return result, nil
}
