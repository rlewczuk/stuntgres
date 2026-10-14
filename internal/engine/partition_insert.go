package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type partitionInsertState struct {
	catalog     CatalogRead
	auth        AuthorizationView
	target      *relationDescriptor
	destination *relationDescriptor
	layouts     map[ObjectAddress]*inheritanceQuerySegment
	physicalRow []sqltype.Value
	namedRow    []sqltype.Value
	groups      map[ObjectAddress]*partitionInsertGroup
	order       []*partitionInsertGroup
}
type partitionInsertGroup struct {
	ref    ObjectRef
	rows   [][]sqltype.Value
	unique *uniqueWriteBinding
}

func (s *StatementScope) capturePartitionInsertLocked(e *Engine, auth AuthorizationView, target *relationDescriptor) error {
	if !hierarchyRelation(target) {
		return nil
	}
	state := &partitionInsertState{catalog: auth.catalog, auth: auth, target: target}
	var captureLayouts func(*relationDescriptor) error
	captureLayout := func(d *relationDescriptor) error {
		if s.output.RegTS == nil && d.partition != nil {
			for _, key := range d.partition.keys {
				if key.usage.Type.ContainsRegTS() {
					if err := e.captureCommentSnapshotLocked(s); err != nil {
						return err
					}
					break
				}
			}
		}
		layout, err := inheritanceQueryLayout(d, target)
		if err != nil {
			return err
		}
		if layout != nil {
			if state.layouts == nil {
				state.layouts = make(map[ObjectAddress]*inheritanceQuerySegment)
			}
			state.layouts[d.address] = &inheritanceQuerySegment{layout: layout, scratch: make([]sqltype.Value, len(layout))}
		}
		return nil
	}
	captureLayouts = func(d *relationDescriptor) error {
		if err := captureLayout(d); err != nil {
			return err
		}
		if d.partition != nil {
			for _, address := range d.partition.children {
				if err := captureLayouts(auth.catalog.state.descriptors[address].(*relationDescriptor)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := captureLayouts(target); err != nil {
		return err
	}
	for child := target; child.membership != nil; {
		parent := auth.catalog.state.descriptors[child.membership.parent].(*relationDescriptor)
		if err := captureLayout(parent); err != nil {
			return err
		}
		child = parent
	}
	if err := partitionPhysicalDescendants(auth.catalog, target, func(leaf *relationDescriptor) error {
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

func (s *StatementScope) validatePartitionInsert(b binding, row []sqltype.Value, evaluation *EvalContext) error {
	state := s.partitionInsert
	state.namedRow = row
	if state.target.partition != nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	destination := state.target
	for destination.partition != nil {
		var values [32]partitionDatum
		key, null, err := partitionRowKey(destination, state.rowFor(destination, row), evaluation, values[:0])
		if err != nil {
			return err
		}
		var selected *relationDescriptor
		for _, address := range destination.partition.children {
			child := state.catalog.state.descriptors[address].(*relationDescriptor)
			if child.membership.defaultBound {
				continue
			}
			contains, err := partitionRangeContains(evaluation.Context, destination.partition.keys, child.membership, key, null, s.input)
			if err != nil {
				return err
			}
			if contains {
				selected = child
				break
			}
		}
		if selected == nil && destination.partition.defaultChild != (ObjectAddress{}) {
			selected = state.catalog.state.descriptors[destination.partition.defaultChild].(*relationDescriptor)
		}
		if selected == nil {
			return state.missingPartition(evaluation.Context, destination, key, s.output)
		}
		destination = selected
	}
	ref := ObjectRef{owner: state.catalog.owner.owner, Epoch: state.catalog.Epoch(), Address: destination.address}
	if state.groups[destination.address] == nil {
		if err := s.acquirePartitionLock(ref, rowExclusive); err != nil {
			return err
		}
	}
	leafBinding := b
	leafBinding.relation, leafBinding.ref = destination, ref
	schema := state.catalog.state.descriptors[destination.namespace].Name()
	physicalRow := state.rowFor(destination, row)
	if err := s.op.session.Engine().validatePhysicalInsertRow(s, leafBinding, &b, schema, physicalRow, evaluation); err != nil {
		return err
	}
	if state.target.partition == nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	state.destination = destination
	state.physicalRow = physicalRow
	return nil
}

func (s *StatementScope) validateTargetPartitionConstraint(b binding, row []sqltype.Value, evaluation *EvalContext) error {
	state := s.partitionInsert
	for child := state.target; child.membership != nil; {
		parent := state.catalog.state.descriptors[child.membership.parent].(*relationDescriptor)
		accepts, err := partitionAccepts(state.catalog, parent, child, state.rowFor(parent, row), evaluation)
		if err != nil {
			return err
		}
		if !accepts {
			err := sqlError("23514", fmt.Sprintf(`new row for relation "%s" violates partition constraint`, state.target.name)).(*sqlstate.Error)
			err.Schema, err.Table = state.catalog.state.descriptors[state.target.namespace].Name(), state.target.name
			detail, detailErr := s.op.session.Engine().rowConstraintDetail(s, &b, row)
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

func (state *partitionInsertState) missingPartition(ctx context.Context, parent *relationDescriptor, key []partitionDatum, output sqltype.OutputEnvironment) error {
	ref := ObjectRef{owner: state.catalog.owner.owner, Epoch: state.catalog.Epoch(), Address: parent.address}
	err := sqlError("23514", fmt.Sprintf(`no partition of relation "%s" found for row`, parent.name)).(*sqlstate.Error)
	err.Schema, err.Table = state.catalog.state.descriptors[parent.namespace].Name(), parent.name
	// Routing/integrity errors remain real, but an actor subject to RLS must
	// not receive key values even when ordinary SELECT ACLs are present.
	decision, securityErr := rowSecurityDecision(state.auth, state.target, state.auth.state.CurrentUser, policyInsert, true)
	if securityErr != nil {
		return securityErr
	}
	if decision != rlsUnrestricted {
		return err
	}
	rights, checkErr := state.auth.Effective(ref, false)
	if checkErr != nil {
		return checkErr
	}
	if rights&authorization.SelectPrivilege == 0 {
		for _, entry := range parent.partition.keys {
			if entry.derived != nil {
				return err
			}
			column := ref
			column.Address.SubID = entry.attribute
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
	for i, entry := range parent.partition.keys {
		if i != 0 {
			names.WriteString(", ")
			values.WriteString(", ")
		}
		label := entry.label
		if entry.derived != nil {
			if renderer.path == nil {
				path, pathErr := state.catalog.owner.referencePath(state.auth)
				if pathErr != nil {
					return pathErr
				}
				renderer.path = path
			}
			cast, coercion := entry.derived.definition.(coercionExpression)
			regTSCast := coercion && cast.mode == sqltype.Explicit && sqltype.IsRegTS(entry.usage.Type.BaseType().ID())
			keyRenderer := renderer
			if regTSCast {
				// Partition diagnostics group the whole pretty cast expression,
				// not its simple operand, as in PostgreSQL's partition key label.
				keyRenderer.pretty = true
			}
			text, renderErr := keyRenderer.sql(ctx, entry.derived.definition, pgRenderParent{})
			if renderErr != nil {
				return renderErr
			}
			if regTSCast {
				text = "(" + text + ")"
			}
			label = text
		} else {
			label = pgIdentifier(label)
		}
		names.WriteString(label)
		if err := writeDiagnosticValue(ctx, &values, key[i].value, output); err != nil {
			return err
		}
	}
	err.Detail = fmt.Sprintf("Partition key of the failing row contains (%s) = (%s).", names.String(), values.String())
	return err
}

func (state *partitionInsertState) rowFor(d *relationDescriptor, row []sqltype.Value) []sqltype.Value {
	if segment := state.layouts[d.address]; segment != nil {
		return segment.mapValues(row)
	}
	return row
}

// Input tuples already own their payload. Identity layouts borrow them. A
// reordered tuple can reuse that payload when there is no logical RETURNING
// consumer; otherwise storage must own a separate immutable physical tuple.
func (s *StatementScope) partitionStoredRow(row []sqltype.Value) []sqltype.Value {
	state := s.partitionInsert
	if state.layouts[state.destination.address] == nil {
		return row
	}
	if s.insertReturning != nil {
		return slices.Clone(state.physicalRow)
	}
	copy(row, state.physicalRow)
	return row
}

func (s *StatementScope) checkInsertUniqueRow(ref ObjectRef, row []sqltype.Value, prefix [][]sqltype.Value) error {
	state := s.partitionInsert
	if state == nil {
		return s.checkUniqueRow(ref, row, nil, prefix)
	}
	address := state.destination.address
	group := state.groups[address]
	if group == nil {
		group = &partitionInsertGroup{ref: ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: address}}
		if state.groups == nil {
			state.groups = make(map[ObjectAddress]*partitionInsertGroup)
		}
		state.groups[address] = group
		state.order = append(state.order, group)
	}
	s.uniqueBinding = group.unique
	row = s.partitionStoredRow(row)
	if err := s.checkUniqueRow(group.ref, row, nil, group.rows); err != nil {
		return err
	}
	group.unique = s.uniqueBinding
	group.rows = append(group.rows, row)
	return nil
}

func (s *StatementScope) applyInsertValidated(ref ObjectRef, rows [][]sqltype.Value) (int, error) {
	if s.partitionInsert == nil {
		return s.Writes().applyInsertValidated(ref, rows)
	}
	count := 0
	for _, group := range s.partitionInsert.order {
		s.uniqueBinding = group.unique
		if err := s.Writes().applyValidated(group.ref, group.rows); err != nil {
			return 0, err
		}
		count += len(group.rows)
	}
	return count, nil
}
