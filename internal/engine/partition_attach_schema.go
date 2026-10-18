package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func partitionAttachKindError(d *relationDescriptor) error {
	kind := "relations"
	switch d.relationKind {
	case tableRelation:
		kind = "tables"
	case viewRelation:
		kind = "views"
	case materializedViewRelation:
		kind = "materialized views"
	case sequenceRelation:
		kind = "sequences"
	case indexRelation:
		kind = "indexes"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action ATTACH PARTITION cannot be performed on relation "%s"`, d.name), Detail: "This operation is not supported for " + kind + "."}
}

func partitionAttachEligibility(catalog CatalogRead, parent, child *relationDescriptor) error {
	if child.relationKind != tableRelation && child.relationKind != partitionedRelation {
		return partitionAttachKindError(child)
	}
	for ancestor := parent; ancestor != nil; {
		if ancestor.address == child.address {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: "circular inheritance not allowed", Detail: fmt.Sprintf(`"%s" is already a child of "%s".`, parent.name, child.name)}
		}
		if ancestor.membership == nil {
			break
		}
		ancestor, _ = catalog.state.descriptors[ancestor.membership.parent].(*relationDescriptor)
	}
	if child.membership != nil {
		return sqlError("42809", fmt.Sprintf(`"%s" is already a partition`, child.name))
	}
	if child.ofType != (ids.ObjectAddress{}) {
		return sqlError("42809", "cannot attach a typed table as partition")
	}
	if child.inheritance != nil && len(child.inheritance.parents) != 0 {
		return sqlError("42809", "cannot attach inheritance child as partition")
	}
	if inheritanceHasChildren(catalog, child.address) {
		return sqlError("42809", "cannot attach inheritance parent as partition")
	}
	parentNamespace := catalog.state.descriptors[parent.namespace].(*namespaceDescriptor)
	childNamespace := catalog.state.descriptors[child.namespace].(*namespaceDescriptor)
	if parentNamespace.temporary == (SessionToken{}) && childNamespace.temporary != (SessionToken{}) {
		return sqlError("42809", fmt.Sprintf(`cannot attach a temporary relation as partition of permanent relation "%s"`, parent.name))
	}
	for _, attribute := range child.attributes {
		if _, ok := child.identities[attribute]; ok {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`table "%s" being attached contains an identity column "%s"`, child.name, child.columns[child.indexForAttribute(attribute)].Name), Detail: "The new partition may not contain an identity column."}
		}
	}
	// Existing FK execution boundaries precede publication.
	for _, descriptor := range catalog.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, foreign := range table.foreignKeys {
			if table.address == child.address || foreign.Target == child.address {
				return sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
			}
		}
	}
	return nil
}

func samePartitionColumnUsage(a, b sqltype.Usage) bool {
	return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation.ID == b.Collation.ID
}

func partitionAttachSchema(ctxScope *StatementScope, catalog CatalogRead, parent, child *relationDescriptor) (*relationDescriptor, error) {
	for _, column := range child.columns {
		if _, ok := parent.index[column.Name]; !ok {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`table "%s" contains column "%s" not found in parent "%s"`, child.name, column.Name, parent.name), Detail: "The new partition may contain only the columns present in parent."}
		}
	}
	in := createLikeInput{source: parent, targetSlots: make([]int, len(parent.columns))}
	for i, column := range parent.columns {
		slot, ok := child.index[column.Name]
		if !ok {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing column "%s"`, column.Name))
		}
		other := child.columns[slot]
		if column.Usage.Type != other.Usage.Type || column.Usage.Typmod != other.Usage.Typmod {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, child.name, column.Name))
		}
		if column.Usage.Collation.ID != other.Usage.Collation.ID {
			return nil, sqlError("42P21", fmt.Sprintf(`child table "%s" has different collation for column "%s"`, child.name, column.Name))
		}
		if !column.Nullable && other.Nullable {
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table "%s" must be marked NOT NULL`, column.Name, child.name))
		}
		_, generated := child.generations[child.attributes[slot]]
		_, requiredGeneration := parent.generations[parent.attributes[i]]
		if generated && !requiredGeneration {
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table must not be a generated column`, column.Name))
		}
		// Generated partitioned definitions remain an inherited admission limit;
		// never erase a stored recipe to make an attachment appear compatible.
		if requiredGeneration {
			return nil, sqlError("0A000", "partitioned table constraint combination is unsupported")
		}
		in.targetSlots[i] = slot
	}
	for _, required := range parent.notNullConstraints {
		parentSlot := parent.indexForAttribute(required.Attribute)
		attribute := child.attributes[in.targetSlots[parentSlot]]
		at := slices.IndexFunc(child.notNullConstraints, func(c notNullConstraint) bool { return c.Attribute == attribute })
		if at >= 0 && !required.NotValid && child.notNullConstraints[at].NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, child.notNullConstraints[at].Name, child.name))
		}
	}
	for _, required := range parent.checkConstraints {
		if required.NoInherit {
			continue
		}
		at := slices.IndexFunc(child.checkConstraints, func(c checkConstraint) bool { return c.Name == required.Name })
		if at < 0 {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing constraint "%s"`, required.Name))
		}
		actual := child.checkConstraints[at]
		mapped, err := in.remap(required.expression, child, catalog)
		if err != nil {
			return nil, err
		}
		binder := expressionBinder{checkDefinitionEquality: true}
		if !binder.sameGroupExpression(mapped, actual.expression) {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different definition for check constraint "%s"`, child.name, required.Name))
		}
		if actual.NoInherit {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on child table "%s"`, required.Name, child.name))
		}
		if !required.NotValid && actual.NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, required.Name, child.name))
		}
	}
	if err := ctxScope.op.Context().Err(); err != nil {
		return nil, err
	}
	next := *child
	// PostgreSQL turns matched existing NN/CHECK facts into inherited facts, but
	// retains their identities and names. NN matching is by mapped attribute.
	next.notNullConstraints = slices.Clone(child.notNullConstraints)
	for i := range next.notNullConstraints {
		constraint := &next.notNullConstraints[i]
		slot := child.indexForAttribute(constraint.Attribute)
		parentSlot := parent.index[child.columns[slot].Name]
		if !parent.columns[parentSlot].Nullable {
			constraint.partitionLocal = false
		}
	}
	return &next, nil
}
