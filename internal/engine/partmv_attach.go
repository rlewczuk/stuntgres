package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CheckAttachTarget rejects ATTACH PARTITION on a relation that is not
// partitioned, then hash partition attachment outside a Restore replay.
func CheckAttachTarget(parent *catalog.Relation, replay bool) error {
	if parent.Partition == nil {
		return partition.AttachKindError(parent)
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
func ValidateAttachRows(ctx context.Context, evaluation *exec.Context, rows *visibility.View, view catalog.Read, parent *catalog.Relation, bound *catalog.PartitionMembership, jobs []partition.AttachScan) error {
	for _, job := range jobs {
		constraints := make([]partition.AttachRowConstraint, 0, 2)
		add := func(p *catalog.Relation, m *catalog.PartitionMembership) error {
			layout, err := catalog.InheritanceQueryLayout(p, job.Leaf)
			if err != nil {
				return err
			}
			constraint := partition.AttachRowConstraint{Parent: p, Child: catalog.Relation{Membership: m}, Layout: layout}
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
				accepts, err := partition.Accepts(view, constraint.Parent, &constraint.Child, values, evaluation)
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
	nextParent, err := partition.WithChild(parent, child.Address(), bound.DefaultBound)
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
