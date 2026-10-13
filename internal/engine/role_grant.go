package engine

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func grantWarning(op *Operation, relation, column string, requested, available authorization.Privileges, all, revoke bool) {
	if available == requested || available != 0 && all {
		return
	}
	message, code := "no privileges were granted", "01007"
	if available != 0 {
		message = "not all privileges were granted"
	}
	if revoke {
		code = "01006"
		message = "no privileges could be revoked"
		if available != 0 {
			message = "not all privileges could be revoked"
		}
	}
	if column == "" {
		message += fmt.Sprintf(` for "%s"`, relation)
	} else {
		message += fmt.Sprintf(` for column "%s" of relation "%s"`, column, relation)
	}
	op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message})
}

func (e *Engine) objectGrantTargets(op *Operation, v AuthorizationView, s *parser.ObjectGrant) ([]ObjectAddress, error) {
	targets := make([]ObjectAddress, 0, len(s.Relations))
	for _, name := range s.Relations {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		ref, err := e.resolveRelationReference(v, name, true)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ref.Address)
	}
	for _, name := range s.Schemas {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		addr := v.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Text}]
		if name.Text == "pg_temp" {
			addr = temporaryNamespace(v)
		}
		if addr == (ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Text))
		}
		ref := ObjectRef{owner: e.owner, Epoch: v.catalog.Epoch(), Address: addr}
		if err := v.Check(AccessRequirement{Object: ref, Privileges: authorization.UsagePrivilege}); err != nil {
			return nil, err
		}
		start := len(targets)
		for address, descriptor := range v.catalog.state.descriptors {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			relation, ok := descriptor.(*relationDescriptor)
			if ok && relation.namespace == addr && !relation.virtualView && (relation.relationKind == tableRelation || relation.relationKind == partitionedRelation || relation.relationKind == viewRelation || relation.relationKind == materializedViewRelation) {
				targets = append(targets, address)
			}
		}
		// Catalog enumeration must not expose Go map iteration as notice order.
		sort.Slice(targets[start:], func(i, j int) bool { return targets[start+i].ObjectID < targets[start+j].ObjectID })
	}
	return targets, nil
}

type columnGrantRequest struct {
	sub  int32
	name string
	mask authorization.Privileges
}
type objectGrantChange struct {
	relation     *relationDescriptor
	columnsOwned bool
}

func (e *Engine) objectGrantLocked(op *Operation, v AuthorizationView, s *parser.ObjectGrant) (Result, error) {
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(v.roles, v.state, *s.Grantor)
		if err != nil {
			return Result{}, err
		}
		if grantor != v.state.CurrentUser {
			return Result{}, sqlError("0A000", "grantor must be current user")
		}
	}
	if s.Kind != "" {
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	targets, err := e.objectGrantTargets(op, v, s)
	if err != nil {
		return Result{}, err
	}
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			grantees[i] = authorization.ACLGrantee{Public: true}
			continue
		}
		role, err := authorization.MembershipRole(v.roles, v.state, spec.Role)
		if err != nil {
			return Result{}, err
		}
		grantees[i] = authorization.ACLGrantee{Role: role}
	}
	masks := make([]authorization.Privileges, len(s.Privileges))
	var tableMask authorization.Privileges
	tableAll := false
	for i, item := range s.Privileges {
		masks[i], err = authorization.ObjectPrivilege(item)
		if err != nil {
			return Result{}, err
		}
		if item.Columns == nil {
			tableMask |= masks[i]
			tableAll = tableAll || item.All
		}
	}
	if s.GrantOption {
		for _, grantee := range grantees {
			if grantee.Public {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
		}
	}
	actor, err := v.roles.Descriptor(v.state.CurrentUser)
	if err != nil {
		return Result{}, err
	}
	principals, err := v.roles.RoleClosure(op.Context(), actor.Ref, true)
	if err != nil {
		return Result{}, err
	}
	var candidate *catalogState
	changed := make(map[ObjectAddress]objectGrantChange)
	for _, address := range targets {
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		relation := v.catalog.state.descriptors[address].(*relationDescriptor)
		prior := changed[address]
		if prior.relation != nil {
			relation = prior.relation
		}
		switch relation.relationKind {
		case sequenceRelation:
			return Result{}, sqlError("0A000", "statement is not supported")
		case indexRelation:
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is an index`, relation.name))
		case compositeRelation:
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, relation.name))
		case catalogRelation:
			return Result{}, sqlError("0A000", "statement is not supported")
		}
		columns := make([]columnGrantRequest, 0)
		addColumn := func(sub int32, name string, mask authorization.Privileges) {
			for i := range columns {
				if columns[i].sub == sub {
					columns[i].mask |= mask
					return
				}
			}
			columns = append(columns, columnGrantRequest{sub: sub, name: name, mask: mask})
		}
		if s.Revoke && tableMask&authorization.ColumnGrantPrivileges != 0 {
			if relation.relationKind != viewRelation {
				for i, name := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid"} {
					addColumn(int32(-6+i), name, tableMask&authorization.ColumnGrantPrivileges)
				}
			}
			for i, column := range relation.columns {
				addColumn(relation.attributes[i], column.Name, tableMask&authorization.ColumnGrantPrivileges)
			}
		}
		for i, item := range s.Privileges {
			for _, column := range item.Columns {
				index, ok := relation.index[column.Text]
				if !ok {
					return Result{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column.Text, relation.name))
				}
				addColumn(relation.attributes[index], column.Text, masks[i])
			}
		}
		// Release traversal uses signed stable attributes, after merging all
		// textual requests (including implicit table-revoke column masks).
		slices.SortFunc(columns, func(a, b columnGrantRequest) int { return cmp.Compare(a.sub, b.sub) })
		originalTable := relation.acl
		nextTable := relation.acl
		var tableChanged bool
		var hasRelationPrivileges, relationPrivilegesChecked bool
		if tableMask != 0 {
			source, available, sourceErr := authorization.ObjectGrantSource(op.Context(), relation.owner, originalTable, authorization.ACLValue{}, principals, actor.Superuser, tableMask)
			if sourceErr != nil {
				return Result{}, sourceErr
			}
			if available == 0 {
				hasRelationPrivileges = authorization.ACLRights(originalTable, relation.owner, principals, authorization.TableGrantPrivileges, false) != 0 || predefinedPrivileges(relation, principals) != 0
				relationPrivilegesChecked = true
				if !hasRelationPrivileges {
					return Result{}, sqlError("42501", "permission denied for table "+relation.name)
				}
			}
			grantWarning(op, relation.name, "", tableMask, available, tableAll, s.Revoke)
			if available != 0 {
				nextTable, tableChanged, err = authorization.ChangeObjectACL(op.Context(), v.roles, authorization.MaterializeRelationACL(originalTable, relation.owner), relation.owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			}
			if err != nil {
				return Result{}, err
			}
			tableChanged = tableChanged || available != 0 && !relation.acl.Explicit
		}
		var nextColumns map[int32]authorization.ACLValue
		for _, column := range columns {
			old := relation.columnACL[column.sub]
			source, available, sourceErr := authorization.ObjectGrantSource(op.Context(), relation.owner, originalTable, old, principals, actor.Superuser, column.mask)
			if sourceErr != nil {
				return Result{}, sourceErr
			}
			if available == 0 {
				if !relationPrivilegesChecked {
					hasRelationPrivileges = authorization.ACLRights(originalTable, relation.owner, principals, authorization.TableGrantPrivileges, false) != 0 || predefinedPrivileges(relation, principals) != 0
					relationPrivilegesChecked = true
				}
				if !hasRelationPrivileges && authorization.ACLRights(old, authorization.PrincipalRef{}, principals, authorization.ColumnGrantPrivileges, false) == 0 {
					return Result{}, sqlError("42501", fmt.Sprintf(`permission denied for column "%s" of relation "%s"`, column.name, relation.name))
				}
			}
			grantWarning(op, relation.name, column.name, column.mask, available, column.mask == authorization.ColumnGrantPrivileges, s.Revoke)
			// System attributes participate in release diagnostics, not native storage.
			if column.sub <= 0 {
				continue
			}
			next, didChange, err := authorization.ChangeObjectACL(op.Context(), v.roles, old, relation.owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			if err != nil {
				return Result{}, err
			}
			if didChange {
				if nextColumns == nil {
					if prior.columnsOwned {
						nextColumns = relation.columnACL
					} else {
						nextColumns = make(map[int32]authorization.ACLValue, len(relation.columnACL)+1)
						for sub, acl := range relation.columnACL {
							nextColumns[sub] = acl
						}
					}
				}
				nextColumns[column.sub] = next
			}
		}
		if tableChanged || nextColumns != nil {
			next := prior.relation
			if next == nil {
				copy := *relation
				next = &copy
			}
			changed[address] = objectGrantChange{relation: next, columnsOwned: prior.columnsOwned || nextColumns != nil}
			next.acl = nextTable
			if nextColumns != nil {
				next.columnACL = nextColumns
			}
			if err := validateACL(v.roles, next); err != nil {
				return Result{}, err
			}
			if candidate == nil {
				candidate = cloneCatalog(v.catalog.state)
			}
			candidate.descriptors[address] = next
		}
	}
	if candidate == nil {
		return Result{}, op.Context().Err()
	}
	if v.roleRecord.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	roles := advanceRoleRevision(v.roleRecord)
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if op.session.transactions.retainedMessage {
		changes := cloneCatalogChanges(op.session.transactions.catalog)
		// Completed grant utilities retain their consulted DB predecessor in
		// the candidate, not a scope authority with a pre-advance role revision.
		if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ObjectRef{}, nil), changes); err != nil {
			return Result{}, err
		}
		for address, change := range changed {
			e.catalogChangeLocked(changes, address, change.relation)
		}
		op.session.transactions.catalog = changes
	} else {
		e.state = candidate
	}
	e.installOperationRolesLocked(op, roles)
	return Result{}, nil
}
