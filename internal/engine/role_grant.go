package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) objectGrantTargets(op *session.Operation, v AuthorizationView, s *parser.ObjectGrant) ([]catalog.Descriptor, error) {
	targets := make([]catalog.Descriptor, 0, len(s.Relations)+len(s.Routines))
	for _, name := range s.Relations {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		if s.Kind == "parameter" {
			key, err := e.canonicalGrantParameter(catalog.ObjectNameText(name))
			if err != nil {
				return nil, err
			}
			duplicate := false
			for _, target := range targets {
				if target.Name() == key {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			descriptor, err := e.grantParameterDescriptorLocked(v, key)
			if err != nil {
				return nil, err
			}
			targets = append(targets, descriptor)
			continue
		}
		ref, err := e.resolveGrantObject(v, name, s.Kind)
		if err != nil {
			return nil, err
		}
		targets = append(targets, v.Catalog.Get(ref.Address))
	}
	for _, target := range s.Routines {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		ref, err := e.resolveGrantRoutine(op, v, target, s.Kind)
		if err != nil {
			return nil, err
		}
		targets = append(targets, v.Catalog.Get(ref.Address))
	}
	for _, name := range s.Schemas {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		addr := v.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Text})
		if name.Text == "pg_temp" {
			addr = temporaryNamespace(v)
		}
		if addr == (ids.ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Text))
		}
		ref := e.catalog.Domain.Ref(v.Catalog.Epoch(), addr)
		if err := v.Check(access.Requirement{Object: ref, Privileges: authorization.UsagePrivilege}); err != nil {
			return nil, err
		}
		start := len(targets)
		for _, descriptor := range v.Catalog.Descriptors() {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if procedure, ok := descriptor.(*catalog.Procedure); ok {
				if procedure.Namespace() == addr && routine.GrantRoutineKindMatches(s.Kind, procedure.RoutineKind) {
					targets = append(targets, descriptor)
				}
				continue
			}
			relation, ok := descriptor.(*catalog.Relation)
			matches := ok && (s.Kind == "" || s.Kind == "sequence") && relation.Namespace() == addr && !relation.VirtualView
			if matches && s.Kind == "sequence" {
				matches = relation.RelationKind == catalog.SequenceRelation
			} else if matches {
				matches = relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.MaterializedViewRelation || relation.RelationKind == catalog.ForeignRelation
			}
			if matches {
				targets = append(targets, descriptor)
			}
		}
		// Catalog enumeration must not expose Go map iteration as notice order.
		sort.Slice(targets[start:], func(i, j int) bool { return targets[start+i].Address().ObjectID < targets[start+j].Address().ObjectID })
	}
	return targets, nil
}

// objectGrantLocked runs GRANT or REVOKE on objects: it validates the
// request, plans the ACL changes, emits their warnings and installs the
// candidate catalog with an advanced role record.
func (e *Engine) objectGrantLocked(op *session.Operation, v AuthorizationView, s *parser.ObjectGrant) (Result, error) {
	req, err := objectGrantRequest(op.Context(), v.View, s, func() ([]catalog.Descriptor, error) { return e.objectGrantTargets(op, v, s) })
	if err != nil {
		return Result{}, err
	}
	plan, warnings, err := objectGrantPlan(op.Context(), v.View, s, req, e.catalog.revisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return Result{}, err
	}
	candidate, changed := plan.candidate, plan.changed
	if candidate == nil {
		return Result{}, op.Context().Err()
	}
	if s.Kind == "schema" {
		if err := candidate.AdvanceNamespaceGeneration(); err != nil {
			return Result{}, err
		}
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	roles := v.RoleRecord.Advance()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if op.Session.Transactions.RetainsTransaction() {
		changes := op.Session.Transactions.Catalog.Clone()
		// Completed grant utilities retain their consulted DB predecessor in
		// the candidate, not a scope authority with a pre-advance role revision.
		if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ids.ObjectRef{}, nil), changes); err != nil {
			return Result{}, err
		}
		for address, change := range changed {
			e.catalogChangeLocked(changes, address, change.descriptor)
		}
		op.Session.Transactions.Catalog = changes
	} else {
		e.catalog.state = candidate
	}
	e.installOperationRolesLocked(op, roles)
	return Result{}, nil
}
