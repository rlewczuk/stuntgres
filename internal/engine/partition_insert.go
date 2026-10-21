package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type partitionInsertState struct {
	catalog     catalog.Read
	auth        AuthorizationView
	target      *catalog.Relation
	destination *catalog.Relation
	layouts     map[ids.ObjectAddress]*exec.InheritanceQuerySegment
	physicalRow []sqltype.Value
	namedRow    []sqltype.Value
	groups      map[ids.ObjectAddress]*partitionInsertGroup
	order       []*partitionInsertGroup
}
type partitionInsertGroup struct {
	ref    ids.ObjectRef
	rows   indexWriteBatch
	unique *uniqueWriteBinding
}

func (s *StatementScope) capturePartitionInsertLocked(e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	if !catalog.HierarchyRelation(target) {
		return nil
	}
	state := &partitionInsertState{catalog: auth.Catalog, auth: auth, target: target}
	var captureLayouts func(*catalog.Relation) error
	captureLayout := func(d *catalog.Relation) error {
		if s.output.RegTS == nil && d.Partition != nil {
			for _, key := range d.Partition.Keys {
				if key.Usage.Type.ContainsRegTS() {
					if err := e.captureCommentSnapshotLocked(s); err != nil {
						return err
					}
					break
				}
			}
		}
		layout, err := catalog.InheritanceQueryLayout(d, target)
		if err != nil {
			return err
		}
		if layout != nil {
			if state.layouts == nil {
				state.layouts = make(map[ids.ObjectAddress]*exec.InheritanceQuerySegment)
			}
			state.layouts[d.Address()] = &exec.InheritanceQuerySegment{Layout: layout, Scratch: make([]sqltype.Value, len(layout))}
		}
		return nil
	}
	captureLayouts = func(d *catalog.Relation) error {
		if err := captureLayout(d); err != nil {
			return err
		}
		if d.Partition != nil {
			for _, address := range d.Partition.Children {
				if err := captureLayouts(auth.Catalog.MustRelation(address)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := captureLayouts(target); err != nil {
		return err
	}
	for child := target; child.Membership != nil; {
		parent := auth.Catalog.MustRelation(child.Membership.Parent)
		if err := captureLayout(parent); err != nil {
			return err
		}
		child = parent
	}
	if err := partitionPhysicalDescendants(auth.Catalog, target, func(leaf *catalog.Relation) error {
		if err := e.captureCheckCommentSnapshotLocked(s, leaf); err != nil {
			return err
		}
		return e.captureCheckVisibilitySnapshotLocked(s, leaf)
	}); err != nil {
		return err
	}
	s.partitionInsert = state
	return nil
}

func (s *StatementScope) validatePartitionInsert(b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	state := s.partitionInsert
	state.namedRow = row
	if state.target.Partition != nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	destination := state.target
	for destination.Partition != nil {
		var values [32]catalog.PartitionDatum
		key, err := partitionRowKey(destination, state.rowFor(destination, row), evaluation, values[:0])
		if err != nil {
			return err
		}
		var selected *catalog.Relation
		for _, address := range destination.Partition.Children {
			child := state.catalog.MustRelation(address)
			if child.Membership.DefaultBound {
				continue
			}
			contains, err := partitionContains(evaluation.Context, destination.Partition.Keys, child.Membership, key, s.input)
			if err != nil {
				return err
			}
			if contains {
				selected = child
				break
			}
		}
		if selected == nil && destination.Partition.DefaultChild != (ids.ObjectAddress{}) {
			selected = state.catalog.MustRelation(destination.Partition.DefaultChild)
		}
		if selected == nil {
			return state.missingPartition(evaluation.Context, destination, key.values, s.output)
		}
		destination = selected
	}
	ref := state.catalog.Owner().Domain.Ref(state.catalog.Epoch(), destination.Address())
	if state.groups[destination.Address()] == nil {
		if err := s.acquirePartitionLock(ref, lock.RowExclusive); err != nil {
			return err
		}
	}
	leafBinding := b
	leafBinding.Relation, leafBinding.Ref = destination, ref
	schema := state.catalog.Get(destination.Namespace()).Name()
	physicalRow := state.rowFor(destination, row)
	if err := s.engine().validatePhysicalInsertRow(s, leafBinding, &b, schema, physicalRow, evaluation); err != nil {
		return err
	}
	if state.target.Partition == nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	state.destination = destination
	state.physicalRow = physicalRow
	return nil
}

func (s *StatementScope) validateTargetPartitionConstraint(b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	state := s.partitionInsert
	for child := state.target; child.Membership != nil; {
		parent := state.catalog.MustRelation(child.Membership.Parent)
		accepts, err := partitionAccepts(state.catalog, parent, child, state.rowFor(parent, row), evaluation)
		if err != nil {
			return err
		}
		if !accepts {
			err := sqlError("23514", fmt.Sprintf(`new row for relation "%s" violates partition constraint`, state.target.Name())).(*sqlstate.Error)
			err.Schema, err.Table = state.catalog.Get(state.target.Namespace()).Name(), state.target.Name()
			detail, detailErr := s.engine().rowConstraintDetail(s, &b, row)
			if detailErr != nil {
				return detailErr
			}
			err.Detail = detail
			return err
		}
		child = parent
	}
	return nil
}

func (state *partitionInsertState) missingPartition(ctx context.Context, parent *catalog.Relation, key []catalog.PartitionDatum, output sqltype.OutputEnvironment) error {
	ref := state.catalog.Owner().Domain.Ref(state.catalog.Epoch(), parent.Address())
	err := sqlError("23514", fmt.Sprintf(`no partition of relation "%s" found for row`, parent.Name())).(*sqlstate.Error)
	err.Schema, err.Table = state.catalog.Get(parent.Namespace()).Name(), parent.Name()
	// Routing/integrity errors remain real, but an actor subject to RLS must
	// not receive key values even when ordinary SELECT ACLs are present.
	decision, securityErr := access.RowSecurityDecision(state.auth.Roles, state.target, state.auth.State.CurrentUser, catalog.PolicyInsert, true)
	if securityErr != nil {
		return securityErr
	}
	if decision != access.RLSUnrestricted {
		return err
	}
	rights, checkErr := state.auth.Effective(ref, false)
	if checkErr != nil {
		return checkErr
	}
	if rights&authorization.SelectPrivilege == 0 {
		for _, entry := range parent.Partition.Keys {
			if entry.Derived != nil {
				return err
			}
			column := ref
			column.Address.SubID = entry.Attribute
			rights, checkErr = state.auth.Effective(column, false)
			if checkErr != nil {
				return checkErr
			}
			if rights&authorization.SelectPrivilege == 0 {
				return err
			}
		}
	}
	var names, values strings.Builder
	renderer := pgRenderer{view: state.catalog, relation: parent, output: output}
	for i, entry := range parent.Partition.Keys {
		if i != 0 {
			names.WriteString(", ")
			values.WriteString(", ")
		}
		label := entry.Label
		if entry.Derived != nil {
			if renderer.path == nil {
				path, pathErr := state.auth.engine.referencePath(state.auth)
				if pathErr != nil {
					return pathErr
				}
				renderer.path = path
			}
			cast, coercion := entry.Derived.Definition.(plan.CoercionExpr)
			regTSCast := coercion && cast.Mode == sqltype.Explicit && sqltype.IsRegTS(entry.Usage.Type.BaseType().ID())
			keyRenderer := renderer
			if regTSCast {
				// Partition diagnostics group the whole pretty cast expression,
				// not its simple operand, as in PostgreSQL's partition key label.
				keyRenderer.pretty = true
			}
			text, renderErr := keyRenderer.sql(ctx, entry.Derived.Definition, pgRenderParent{})
			if renderErr != nil {
				return renderErr
			}
			if regTSCast {
				text = "(" + text + ")"
			}
			label = text
		} else {
			label = catalog.Identifier(label)
		}
		names.WriteString(label)
		if err := exec.WriteDiagnosticValue(ctx, &values, key[i].Value, output); err != nil {
			return err
		}
	}
	err.Detail = fmt.Sprintf("Partition key of the failing row contains (%s) = (%s).", names.String(), values.String())
	return err
}

func (state *partitionInsertState) rowFor(d *catalog.Relation, row []sqltype.Value) []sqltype.Value {
	if segment := state.layouts[d.Address()]; segment != nil {
		return segment.MapValues(row)
	}
	return row
}

// Input tuples already own their payload. Identity layouts borrow them. A
// reordered tuple can reuse that payload when there is no logical RETURNING
// consumer; otherwise storage must own a separate immutable physical tuple.
func (s *StatementScope) partitionStoredRow(row []sqltype.Value) []sqltype.Value {
	state := s.partitionInsert
	if state.layouts[state.destination.Address()] == nil {
		return row
	}
	if s.insertReturning != nil {
		return slices.Clone(state.physicalRow)
	}
	copy(row, state.physicalRow)
	return row
}

func (s *StatementScope) partitionInsertionGroup(ref ids.ObjectRef) *partitionInsertGroup {
	state := s.partitionInsert
	address := state.destination.Address()
	group := state.groups[address]
	if group == nil {
		group = &partitionInsertGroup{ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.groups == nil {
			state.groups = make(map[ids.ObjectAddress]*partitionInsertGroup)
		}
		state.groups[address] = group
		state.order = append(state.order, group)
	}
	return group
}

func (s *StatementScope) checkInsertUniqueRow(ref ids.ObjectRef, candidate *indexWriteCandidate, prefix indexWriteBatch) error {
	if s.partitionInsert == nil {
		return s.checkUniqueRow(ref, candidate, nil, prefix)
	}
	group := s.partitionInsertionGroup(ref)
	s.uniqueBinding = group.unique
	candidate.values = s.partitionStoredRow(candidate.values)
	if err := s.checkUniqueRow(group.ref, candidate, nil, group.rows); err != nil {
		return err
	}
	group.unique = s.uniqueBinding
	group.rows.append(*candidate)
	return nil
}

func (s *StatementScope) applyInsertValidated(ref ids.ObjectRef, rows indexWriteBatch) (int, error) {
	if s.partitionInsert == nil {
		return s.Writes().applyInsertBatch(ref, rows)
	}
	count := 0
	for _, group := range s.partitionInsert.order {
		s.uniqueBinding = group.unique
		if err := s.Writes().applyValidated(group.ref, group.rows); err != nil {
			return 0, err
		}
		count += len(group.rows.values)
	}
	return count, nil
}
