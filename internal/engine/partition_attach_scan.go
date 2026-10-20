package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type partitionAttachScan struct {
	leaf    *catalog.Relation
	exclude bool
}

// Candidate AX admission is unconditional and already complete. DEFAULT opens
// descendants only when the deciding relation cannot prove its exclusion.
func (s *StatementScope) partitionAttachFrontier(catalogView catalog.Read, parent, target *catalog.Relation, bound *catalog.PartitionMembership, exclude, lockChildren bool) ([]partitionAttachScan, error) {
	var scans []partitionAttachScan
	var walk func(catalog.Read, *catalog.Relation) error
	walk = func(view catalog.Read, d *catalog.Relation) error {
		proven, err := partitionConstraintProven(s, view, parent, d, bound, exclude)
		if err != nil {
			return err
		}
		if proven && !exclude {
			for child := parent; child.Membership != nil; {
				ancestor := view.MustRelation(child.Membership.Parent)
				proven, err = partitionConstraintProven(s, view, ancestor, d, child.Membership, false)
				if err != nil || !proven {
					break
				}
				child = ancestor
			}
		}
		if err != nil {
			return err
		}
		if proven {
			return nil
		}
		if d.Partition == nil {
			scans = append(scans, partitionAttachScan{leaf: d, exclude: exclude})
			return nil
		}
		for _, address := range d.Partition.Children {
			child, ok := view.Relation(address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			childView := view
			if lockChildren {
				ref := s.op.session.Engine().catalog.Domain.Ref(view.Epoch(), address)
				if err := s.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
					return err
				}
				var err error
				childView, child, err = s.op.session.Engine().partitionAttachRelation(s, ref)
				if err != nil {
					return err
				}
			}
			if err := walk(childView, child); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(catalogView, target)
	return scans, err
}

// Fences cover the complete candidate stores even when proof elides a scan.
// Scanned DEFAULT stores have their own predecessors. Normal query snapshots
// are untouched; the dedicated view uses the same owner/command and own rows.
func (e *Engine) partitionAttachCapture(scope *StatementScope, stage *CatalogStage, view catalog.Read, candidate *catalog.Relation, jobs []partitionAttachScan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	capture := func(d *catalog.Relation) error {
		if current := e.privateCatalogLocked(scope.owner.catalog).Get(d.Address()); current != d {
			return ids.ErrCatalogReplaced
		}
		stage.domainBases = append(stage.domainBases, d)
		storage := e.privateStorageLocked(scope.owner.catalog, d.Address())
		if storage == nil {
			return ids.ErrCatalogReplaced
		}
		stage.partitionReads = append(stage.partitionReads, partitionReadFence{address: d.Address(), storage: storage, length: len(storage.Versions), ends: storage.Ends, generation: storage.Generation})
		return nil
	}
	if err := partitionPhysicalDescendants(view, candidate, capture); err != nil {
		return err
	}
	refs := make([]ids.ObjectRef, 0, len(jobs))
	for _, job := range jobs {
		if job.exclude {
			if err := capture(job.leaf); err != nil {
				return err
			}
		}
		refs = append(refs, e.catalog.Domain.Ref(view.Epoch(), job.leaf.Address()))
	}
	if len(refs) == 0 {
		return nil
	}
	_, err := scope.acquirePartitionValidationViewsLocked(e, refs)
	return err
}

type partitionAttachRowConstraint struct {
	parent  *catalog.Relation
	child   catalog.Relation
	layout  []int
	scratch []sqltype.Value
}

func partitionAttachValidateRows(scope *StatementScope, view catalog.Read, parent *catalog.Relation, bound *catalog.PartitionMembership, jobs []partitionAttachScan) error {
	ctx := scope.op.Context()
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, statementScope: scope, Input: scope.input, Output: scope.output}}
	for _, job := range jobs {
		constraints := make([]partitionAttachRowConstraint, 0, 2)
		add := func(p *catalog.Relation, m *catalog.PartitionMembership) error {
			layout, err := inheritanceQueryLayout(p, job.leaf)
			if err != nil {
				return err
			}
			constraint := partitionAttachRowConstraint{parent: p, child: catalog.Relation{Membership: m}, layout: layout}
			if layout != nil {
				constraint.scratch = make([]sqltype.Value, len(layout))
			}
			constraints = append(constraints, constraint)
			return nil
		}
		if err := add(parent, bound); err != nil {
			return err
		}
		if !job.exclude {
			for child := parent; child.Membership != nil; {
				ancestor := view.MustRelation(child.Membership.Parent)
				if err := add(ancestor, child.Membership); err != nil {
					return err
				}
				child = ancestor
			}
		}
		ref := view.Owner().Domain.Ref(view.Epoch(), job.leaf.Address())
		source, err := captureQuerySource(ctx, scope.partitionValidationView, ref)
		if err != nil {
			return err
		}
		for i := range source.count() {
			row := source.row(i)
			for j := range constraints {
				constraint := &constraints[j]
				values := row
				if constraint.layout != nil {
					for slot, physical := range constraint.layout {
						constraint.scratch[slot] = row[physical]
					}
					values = constraint.scratch
				}
				accepts, err := partitionAccepts(view, constraint.parent, &constraint.child, values, &evaluation)
				if err != nil {
					source.close()
					return err
				}
				if accepts != job.exclude {
					continue
				}
				source.close()
				message := fmt.Sprintf(`partition constraint of relation "%s" is violated by some row`, job.leaf.Name())
				if job.exclude {
					message = fmt.Sprintf(`updated partition constraint for default partition "%s" would be violated by some row`, job.leaf.Name())
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: message, Schema: view.Get(job.leaf.Namespace()).Name(), Table: job.leaf.Name()}
			}
		}
		source.close()
	}
	return ctx.Err()
}
