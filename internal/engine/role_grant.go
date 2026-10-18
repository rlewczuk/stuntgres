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

func (e *Engine) objectGrantTargets(op *Operation, v AuthorizationView, s *parser.ObjectGrant) ([]Descriptor, error) {
	targets := make([]Descriptor, 0, len(s.Relations)+len(s.Routines))
	for _, name := range s.Relations {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		if s.Kind == "parameter" {
			key, err := e.canonicalGrantParameter(objectNameText(name))
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
		targets = append(targets, v.catalog.state.descriptors[ref.Address])
	}
	for _, target := range s.Routines {
		if err := op.Context().Err(); err != nil {
			return nil, err
		}
		ref, err := e.resolveGrantRoutine(op, v, target, s.Kind)
		if err != nil {
			return nil, err
		}
		targets = append(targets, v.catalog.state.descriptors[ref.Address])
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
		ref := e.domain.Ref(v.catalog.Epoch(), addr)
		if err := v.Check(AccessRequirement{Object: ref, Privileges: authorization.UsagePrivilege}); err != nil {
			return nil, err
		}
		start := len(targets)
		for _, descriptor := range v.catalog.state.descriptors {
			if err := op.Context().Err(); err != nil {
				return nil, err
			}
			if routine, ok := descriptor.(*procedureDescriptor); ok {
				if routine.namespace == addr && grantRoutineKindMatches(s.Kind, routine.routineKind) {
					targets = append(targets, descriptor)
				}
				continue
			}
			relation, ok := descriptor.(*relationDescriptor)
			matches := ok && (s.Kind == "" || s.Kind == "sequence") && relation.namespace == addr && !relation.virtualView
			if matches && s.Kind == "sequence" {
				matches = relation.relationKind == sequenceRelation
			} else if matches {
				matches = relation.relationKind == tableRelation || relation.relationKind == partitionedRelation || relation.relationKind == viewRelation || relation.relationKind == materializedViewRelation || relation.relationKind == foreignRelation
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

type columnGrantRequest struct {
	sub  int32
	name string
	mask authorization.Privileges
}
type objectGrantChange struct {
	descriptor   Descriptor
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
	unavailable := ""
	switch s.Kind {
	case "language":
		unavailable = "LANGUAGE privileges are unsupported in embedded mode"
	case "foreign data wrapper":
		unavailable = "FOREIGN DATA WRAPPER privileges are unsupported in embedded mode"
	case "foreign server":
		unavailable = "FOREIGN SERVER privileges are unsupported in embedded mode"
	case "large object":
		unavailable = "LARGE OBJECT privileges are unsupported in embedded mode"
	case "", "sequence", "schema", "type", "domain", "function", "procedure", "routine", "parameter", "tablespace":
	default:
		return Result{}, sqlError("0A000", "statement is not supported")
	}
	var targets []Descriptor
	var err error
	if unavailable == "" {
		targets, err = e.objectGrantTargets(op, v, s)
		if err != nil {
			return Result{}, err
		}
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
		kind := s.Kind
		if kind == "" {
			kind = "relation"
		}
		masks[i], err = authorization.ObjectPrivilege(item, kind)
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
	if unavailable != "" {
		return Result{}, sqlError("0A000", unavailable)
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
	for _, target := range targets {
		address := target.Address()
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		if target.Kind() != RelationObject {
			d := target
			if prior := changed[address].descriptor; prior != nil {
				d = prior
			}
			next, err := changeNonRelationGrant(op, v, d, s, grantees, principals, actor.Superuser, tableMask, tableAll)
			if err != nil {
				return Result{}, err
			}
			if next != nil {
				changed[address] = objectGrantChange{descriptor: next}
				if candidate == nil {
					candidate = cloneCatalog(v.catalog.state)
				}
				if parameter, ok := next.(*parameterDescriptor); ok {
					installParameterGrant(candidate, parameter)
					if candidate.descriptors[address] == nil {
						changed[address] = objectGrantChange{}
					}
				} else {
					candidate.descriptors[address] = next
				}
			}
			continue
		}
		relation := target.(*relationDescriptor)
		prior := changed[address]
		if prior.descriptor != nil {
			relation = prior.descriptor.(*relationDescriptor)
		}
		if s.Kind == "sequence" && relation.relationKind != sequenceRelation {
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is not a sequence`, relation.name))
		}
		switch relation.relationKind {
		case indexRelation:
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is an index`, relation.name))
		case compositeRelation:
			return Result{}, sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, relation.name))
		case catalogRelation:
			return Result{}, sqlError("0A000", "statement is not supported")
		}
		classMask := authorization.TableGrantPrivileges
		noun := "table"
		if relation.relationKind == sequenceRelation {
			classMask = authorization.DefaultMask(authorization.DefaultSequences)
			noun = "sequence"
		}
		targetMask := tableMask
		if tableAll {
			targetMask = classMask
		} else if relation.relationKind == sequenceRelation && s.Kind == "" {
			if targetMask & ^classMask != 0 {
				op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: fmt.Sprintf(`sequence "%s" only supports USAGE, SELECT, and UPDATE privileges`, relation.name)})
			}
			targetMask &= classMask
		} else if targetMask & ^classMask != 0 {
			for _, item := range s.Privileges {
				if _, err := authorization.ObjectPrivilege(item, ""); err != nil {
					return Result{}, err
				}
			}
		}
		originalTable := relation.acl
		nextTable := relation.acl
		var tableChanged bool
		if targetMask != 0 {
			source, available, sourceErr := authorization.ObjectGrantSource(op.Context(), relation.owner, originalTable, authorization.ACLValue{}, principals, actor.Superuser, targetMask)
			if sourceErr != nil {
				return Result{}, sourceErr
			}
			if available == 0 {
				hasRelationPrivileges := authorization.ACLRights(originalTable, relation.owner, principals, classMask, false) != 0 || predefinedPrivileges(relation, principals) != 0
				if !hasRelationPrivileges {
					return Result{}, sqlError("42501", "permission denied for "+noun+" "+relation.name)
				}
			}
			grantWarning(op, relation.name, "", targetMask, available, tableAll, s.Revoke)
			if available != 0 {
				materialized := originalTable
				if !materialized.Explicit {
					if relation.relationKind == sequenceRelation {
						materialized = authorization.DefaultBaseline(relation.owner, authorization.DefaultSequences, false)
					} else {
						materialized = authorization.MaterializeRelationACL(originalTable, relation.owner)
					}
				}
				nextTable, tableChanged, err = authorization.ChangeObjectACL(op.Context(), v.roles, materialized, relation.owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			}
			if err != nil {
				return Result{}, err
			}
			tableChanged = tableChanged || available != 0 && !relation.acl.Explicit
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
		if s.Revoke && targetMask&authorization.ColumnGrantPrivileges != 0 {
			if relation.relationKind != viewRelation {
				for i, name := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid"} {
					addColumn(int32(-6+i), name, targetMask&authorization.ColumnGrantPrivileges)
				}
			}
			for i, column := range relation.columns {
				addColumn(relation.attributes[i], column.Name, targetMask&authorization.ColumnGrantPrivileges)
			}
		}
		for i, item := range s.Privileges {
			mask := masks[i]
			if len(item.Columns) != 0 && relation.relationKind == sequenceRelation {
				if mask & ^authorization.SelectPrivilege != 0 {
					op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: fmt.Sprintf(`sequence "%s" only supports SELECT column privileges`, relation.name)})
				}
				mask &= authorization.SelectPrivilege
			}
			for _, column := range item.Columns {
				index, ok := relation.index[column.Text]
				if !ok {
					return Result{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column.Text, relation.name))
				}
				if mask != 0 {
					addColumn(relation.attributes[index], column.Text, mask)
				}
			}
		}
		// Release traversal uses signed stable attributes, after merging all
		// textual requests (including implicit table-revoke column masks).
		slices.SortFunc(columns, func(a, b columnGrantRequest) int { return cmp.Compare(a.sub, b.sub) })
		var nextColumns map[int32]authorization.ACLValue
		var hasColumnPrivileges, columnPrivilegesChecked bool
		for _, column := range columns {
			old := relation.columnACL[column.sub]
			source, available, sourceErr := authorization.ObjectGrantSource(op.Context(), relation.owner, originalTable, old, principals, actor.Superuser, column.mask)
			if sourceErr != nil {
				return Result{}, sourceErr
			}
			if available == 0 {
				if !columnPrivilegesChecked {
					hasColumnPrivileges = authorization.ACLRights(originalTable, relation.owner, principals, authorization.ColumnGrantPrivileges, false)&authorization.ColumnGrantPrivileges != 0 ||
						predefinedPrivileges(relation, principals)&authorization.ColumnGrantPrivileges != 0
					columnPrivilegesChecked = true
				}
				if !hasColumnPrivileges && authorization.ACLRights(old, authorization.PrincipalRef{}, principals, authorization.ColumnGrantPrivileges, false)&authorization.ColumnGrantPrivileges == 0 {
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
				if s.Revoke && len(next.Entries) == 0 {
					delete(nextColumns, column.sub)
				} else {
					nextColumns[column.sub] = next
				}
			}
		}
		if tableChanged || nextColumns != nil {
			next, _ := prior.descriptor.(*relationDescriptor)
			if next == nil {
				copy := *relation
				next = &copy
			}
			changed[address] = objectGrantChange{descriptor: next, columnsOwned: prior.columnsOwned || nextColumns != nil}
			next.acl = nextTable
			if nextColumns != nil {
				next.columnACL = nextColumns
			}
			if err := validateACL(v.roles, next); err != nil {
				return Result{}, err
			}
			if next.relationKind == sequenceRelation {
				stamp, err := e.revisionLocked()
				if err != nil {
					return Result{}, err
				}
				next.sequencePlanStamp = stamp
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
	if s.Kind == "schema" {
		if err := candidate.advanceNamespaceGeneration(); err != nil {
			return Result{}, err
		}
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
	if op.session.transactions.retainsTransaction() {
		changes := cloneCatalogChanges(op.session.transactions.catalog)
		// Completed grant utilities retain their consulted DB predecessor in
		// the candidate, not a scope authority with a pre-advance role revision.
		if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ObjectRef{}, nil), changes); err != nil {
			return Result{}, err
		}
		for address, change := range changed {
			e.catalogChangeLocked(changes, address, change.descriptor)
		}
		op.session.transactions.catalog = changes
	} else {
		e.state = candidate
	}
	e.installOperationRolesLocked(op, roles)
	return Result{}, nil
}
