package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func partitionAttachKindError(d *catalog.Relation) error {
	kind := "relations"
	switch d.RelationKind {
	case catalog.TableRelation:
		kind = "tables"
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.SequenceRelation:
		kind = "sequences"
	case catalog.IndexRelation:
		kind = "indexes"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action ATTACH PARTITION cannot be performed on relation "%s"`, d.Name()), Detail: "This operation is not supported for " + kind + "."}
}

func partitionAttachEligibility(view catalog.Read, parent, child *catalog.Relation) error {
	if child.RelationKind != catalog.TableRelation && child.RelationKind != catalog.PartitionedRelation {
		return partitionAttachKindError(child)
	}
	for ancestor := parent; ancestor != nil; {
		if ancestor.Address() == child.Address() {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: "circular inheritance not allowed", Detail: fmt.Sprintf(`"%s" is already a child of "%s".`, parent.Name(), child.Name())}
		}
		if ancestor.Membership == nil {
			break
		}
		ancestor, _ = view.Relation(ancestor.Membership.Parent)
	}
	if child.Membership != nil {
		return sqlError("42809", fmt.Sprintf(`"%s" is already a partition`, child.Name()))
	}
	if child.OfType != (ids.ObjectAddress{}) {
		return sqlError("42809", "cannot attach a typed table as partition")
	}
	if child.Inheritance != nil && len(child.Inheritance.Parents) != 0 {
		return sqlError("42809", "cannot attach inheritance child as partition")
	}
	if catalog.InheritanceHasChildren(view, child.Address()) {
		return sqlError("42809", "cannot attach inheritance parent as partition")
	}
	parentNamespace := view.MustNamespace(parent.Namespace())
	childNamespace := view.MustNamespace(child.Namespace())
	if parentNamespace.Temporary == (ids.SessionToken{}) && childNamespace.Temporary != (ids.SessionToken{}) {
		return sqlError("42809", fmt.Sprintf(`cannot attach a temporary relation as partition of permanent relation "%s"`, parent.Name()))
	}
	for _, attribute := range child.Attributes {
		if _, ok := child.ColumnMeta.Identities[attribute]; ok {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`table "%s" being attached contains an identity column "%s"`, child.Name(), child.Columns[child.IndexForAttribute(attribute)].Name), Detail: "The new partition may not contain an identity column."}
		}
	}
	// Existing FK execution boundaries precede publication.
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, foreign := range table.Constraints.ForeignKeys {
			if table.Address() == child.Address() || foreign.Target == child.Address() {
				return sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
			}
		}
	}
	return nil
}

func partitionAttachSchema(ctxScope *StatementScope, view catalog.Read, parent, child *catalog.Relation) (*catalog.Relation, error) {
	for _, column := range child.Columns {
		if _, ok := parent.Index[column.Name]; !ok {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`table "%s" contains column "%s" not found in parent "%s"`, child.Name(), column.Name, parent.Name()), Detail: "The new partition may contain only the columns present in parent."}
		}
	}
	in := analyze.LikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
	for i, column := range parent.Columns {
		slot, ok := child.Index[column.Name]
		if !ok {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing column "%s"`, column.Name))
		}
		other := child.Columns[slot]
		if column.Usage.Type != other.Usage.Type || column.Usage.Typmod != other.Usage.Typmod {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, child.Name(), column.Name))
		}
		if column.Usage.Collation.ID != other.Usage.Collation.ID {
			return nil, sqlError("42P21", fmt.Sprintf(`child table "%s" has different collation for column "%s"`, child.Name(), column.Name))
		}
		if !column.Nullable && other.Nullable {
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table "%s" must be marked NOT NULL`, column.Name, child.Name()))
		}
		_, generated := child.ColumnMeta.Generations[child.Attributes[slot]]
		_, requiredGeneration := parent.ColumnMeta.Generations[parent.Attributes[i]]
		if generated && !requiredGeneration {
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table must not be a generated column`, column.Name))
		}
		// Generated partitioned definitions remain an inherited admission limit;
		// never erase a stored recipe to make an attachment appear compatible.
		if requiredGeneration {
			return nil, sqlError("0A000", "partitioned table constraint combination is unsupported")
		}
		in.TargetSlots[i] = slot
	}
	for _, required := range parent.Constraints.NotNull {
		parentSlot := parent.IndexForAttribute(required.Attribute)
		attribute := child.Attributes[in.TargetSlots[parentSlot]]
		at := slices.IndexFunc(child.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute })
		if at >= 0 && !required.NotValid && child.Constraints.NotNull[at].NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, child.Constraints.NotNull[at].Name, child.Name()))
		}
	}
	for _, required := range parent.Constraints.Check {
		if required.NoInherit {
			continue
		}
		at := slices.IndexFunc(child.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == required.Name })
		if at < 0 {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing constraint "%s"`, required.Name))
		}
		actual := child.Constraints.Check[at]
		mapped, err := remapLikeExpression(&in, required.Expression, child, view)
		if err != nil {
			return nil, err
		}
		binder := analyze.Binder{Consumer: analyze.ConsumerContext{CheckDefinitionEquality: true}}
		if !binder.SameGroupExpression(mapped, actual.Expression) {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different definition for check constraint "%s"`, child.Name(), required.Name))
		}
		if actual.NoInherit {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on child table "%s"`, required.Name, child.Name()))
		}
		if !required.NotValid && actual.NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, required.Name, child.Name()))
		}
	}
	if err := ctxScope.op.Context().Err(); err != nil {
		return nil, err
	}
	next := *child
	// PostgreSQL turns matched existing NN/CHECK facts into inherited facts, but
	// retains their identities and names. NN matching is by mapped attribute.
	next.Constraints.NotNull = slices.Clone(child.Constraints.NotNull)
	for i := range next.Constraints.NotNull {
		constraint := &next.Constraints.NotNull[i]
		slot := child.IndexForAttribute(constraint.Attribute)
		parentSlot := parent.Index[child.Columns[slot].Name]
		if !parent.Columns[parentSlot].Nullable {
			constraint.PartitionLocal = false
		}
	}
	return &next, nil
}
