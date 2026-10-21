package partition

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func AttachKindError(d *catalog.Relation) error {
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

func AttachEligibility(view catalog.Read, parent, child *catalog.Relation) error {
	if child.RelationKind != catalog.TableRelation && child.RelationKind != catalog.PartitionedRelation {
		return AttachKindError(child)
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

func AttachSchema(ctx context.Context, view catalog.Read, parent, child *catalog.Relation) (*catalog.Relation, error) {
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
		mapped, err := stored.RemapLikeExpression(&in, required.Expression, child, view)
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
	if err := ctx.Err(); err != nil {
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

type AttachScan struct {
	Leaf    *catalog.Relation
	Exclude bool
}

type AttachRowConstraint struct {
	Parent  *catalog.Relation
	Child   catalog.Relation
	Layout  []int
	Scratch []sqltype.Value
}

// CheckAttachTarget rejects ATTACH PARTITION on a relation that is not
// partitioned, then hash partition attachment outside a Restore replay.
func CheckAttachTarget(parent *catalog.Relation, replay bool) error {
	if parent.Partition == nil {
		return AttachKindError(parent)
	}
	if parent.Partition.Strategy == parser.PartitionHash && !replay {
		return sqlError("0A000", "hash partition attachment is unsupported")
	}
	return nil
}

// ValidateAttachRows scans the leaves of jobs in rows, the captured
// validation view, and rejects a row the new bound (or, for the default
// partition's leaves, its exclusion) does not admit. evaluation evaluates
// partition keys. It holds nothing and never waits.
func ValidateAttachRows(ctx context.Context, evaluation *exec.Context, rows *visibility.View, view catalog.Read, parent *catalog.Relation, bound *catalog.PartitionMembership, jobs []AttachScan) error {
	for _, job := range jobs {
		constraints := make([]AttachRowConstraint, 0, 2)
		add := func(p *catalog.Relation, m *catalog.PartitionMembership) error {
			layout, err := catalog.InheritanceQueryLayout(p, job.Leaf)
			if err != nil {
				return err
			}
			constraint := AttachRowConstraint{Parent: p, Child: catalog.Relation{Membership: m}, Layout: layout}
			if layout != nil {
				constraint.Scratch = make([]sqltype.Value, len(layout))
			}
			constraints = append(constraints, constraint)
			return nil
		}
		if err := add(parent, bound); err != nil {
			return err
		}
		if !job.Exclude {
			for child := parent; child.Membership != nil; {
				ancestor := view.MustRelation(child.Membership.Parent)
				if err := add(ancestor, child.Membership); err != nil {
					return err
				}
				child = ancestor
			}
		}
		ref := view.Owner().Domain.Ref(view.Epoch(), job.Leaf.Address())
		source, err := exec.CaptureSource(ctx, rows, ref)
		if err != nil {
			return err
		}
		for i := range source.Count() {
			row := source.Row(i)
			for j := range constraints {
				constraint := &constraints[j]
				values := row
				if constraint.Layout != nil {
					for slot, physical := range constraint.Layout {
						constraint.Scratch[slot] = row[physical]
					}
					values = constraint.Scratch
				}
				accepts, err := Accepts(view, constraint.Parent, &constraint.Child, values, evaluation)
				if err != nil {
					source.Close()
					return err
				}
				if accepts != job.Exclude {
					continue
				}
				source.Close()
				message := fmt.Sprintf(`partition constraint of relation "%s" is violated by some row`, job.Leaf.Name())
				if job.Exclude {
					message = fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, job.Leaf.Name())
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: message, Schema: view.Get(job.Leaf.Namespace()).Name(), Table: job.Leaf.Name()}
			}
		}
		source.Close()
	}
	return ctx.Err()
}

// AttachDescriptors returns parent with child attached under bound, and
// child (the candidate AttachSchema returned) with its membership; both
// with their revisions bumped.
func AttachDescriptors(parent, child *catalog.Relation, bound *catalog.PartitionMembership) (*catalog.Relation, *catalog.Relation, error) {
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) || child.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	nextChild := *child
	nextChild.Membership = bound
	nextChild.ObjectRevision++
	nextParent, err := WithChild(parent, child.Address(), bound.DefaultBound)
	if err != nil {
		return nil, nil, err
	}
	return nextParent, &nextChild, nil
}

// StageIndexChildren stages the child of every partitioned index of parent
// on the partition at child, in declaration order, re-reading the child
// through the stage after each one. CREATE allows incomplete virtual
// children; ATTACH does not, and then runs the partitioned-index validity
// pass. It runs with Engine.mu held and never waits.
func StageIndexChildren(h index.Host, parent *catalog.Relation, child ids.ObjectAddress, attach bool) error {
	for _, idx := range index.PartitionIndexDeclarationOrder(h.StageBase(), parent) {
		current := h.StageRead().MustRelation(child)
		if _, err := index.StagePartitionIndexChild(h, parent, idx, current, !attach); err != nil {
			return err
		}
	}
	if !attach {
		return nil
	}
	return index.PartitionIndexValidity(h.ValidityTarget(), ids.ObjectAddress{})
}
