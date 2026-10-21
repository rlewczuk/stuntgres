package role

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type columnGrantRequest struct {
	sub  int32
	name string
	mask authorization.Privileges
}

// ObjectGrantChange is the new descriptor of one changed grant target.
type ObjectGrantChange struct {
	Descriptor   catalog.Descriptor
	columnsOwned bool
}

// GrantRequest is a validated GRANT or REVOKE on objects.
type GrantRequest struct {
	targets  []catalog.Descriptor
	grantees []authorization.ACLGrantee
	// masks holds the privileges of each s.Privileges item; tableMask and
	// tableAll combine the items without a column list.
	masks     []authorization.Privileges
	tableMask authorization.Privileges
	tableAll  bool
	// principals is the current user's role closure.
	principals []authorization.PrincipalRef
	superuser  bool
}

// ObjectGrantRequest validates s: grantor, object kind, targets, grantees,
// privileges and GRANT OPTION for PUBLIC, in PostgreSQL's order. The targets
// are resolved only for supported kinds, after the kind check and before
// grantees are resolved.
func ObjectGrantRequest(h Host, auth ddl.Authority, s *parser.ObjectGrant) (GrantRequest, error) {
	ctx, view := h.Context(), h.AccessView(auth)
	if s.Grantor != nil {
		grantor, err := authorization.MembershipRole(view.Roles, view.State, *s.Grantor)
		if err != nil {
			return GrantRequest{}, err
		}
		if grantor != view.State.CurrentUser {
			return GrantRequest{}, sqlError("0A000", "grantor must be current user")
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
	case "", "sequence", "schema", "type", "domain", "function", "procedure", "routine", "parameter", "tablespace", "large object":
	default:
		return GrantRequest{}, sqlError("0A000", "statement is not supported")
	}
	var req GrantRequest
	var err error
	if unavailable == "" {
		req.targets, err = objectGrantTargets(h, auth, s)
		if err != nil {
			return GrantRequest{}, err
		}
	}
	req.grantees = make([]authorization.ACLGrantee, len(s.Grantees))
	for i, spec := range s.Grantees {
		if spec.Public {
			req.grantees[i] = authorization.ACLGrantee{Public: true}
			continue
		}
		role, err := authorization.MembershipRole(view.Roles, view.State, spec.Role)
		if err != nil {
			return GrantRequest{}, err
		}
		req.grantees[i] = authorization.ACLGrantee{Role: role}
	}
	req.masks = make([]authorization.Privileges, len(s.Privileges))
	for i, item := range s.Privileges {
		kind := s.Kind
		if kind == "" {
			kind = "relation"
		}
		req.masks[i], err = authorization.ObjectPrivilege(item, kind)
		if err != nil {
			return GrantRequest{}, err
		}
		if item.Columns == nil {
			req.tableMask |= req.masks[i]
			req.tableAll = req.tableAll || item.All
		}
	}
	if s.GrantOption {
		for _, grantee := range req.grantees {
			if grantee.Public {
				return GrantRequest{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
		}
	}
	if unavailable != "" {
		return GrantRequest{}, sqlError("0A000", unavailable)
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return GrantRequest{}, err
	}
	req.principals, err = view.Roles.RoleClosure(ctx, actor.Ref, true)
	if err != nil {
		return GrantRequest{}, err
	}
	req.superuser = actor.Superuser
	return req, nil
}

// GrantPlan is the catalog change of a GRANT or REVOKE on objects.
type GrantPlan struct {
	// candidate is the next catalog state; nil when nothing changes.
	Candidate *catalog.State
	Changed   map[ids.ObjectAddress]ObjectGrantChange
}

// ObjectGrantPlan builds the new ACLs of req's targets. revision allocates
// the plan stamps of changed sequences. The warnings are returned in
// statement order, also with an error: the ones raised before it.
func ObjectGrantPlan(ctx context.Context, view access.View, s *parser.ObjectGrant, req GrantRequest, revision func() (ids.DefinitionRevision, error)) (GrantPlan, []sqlstate.Error, error) {
	var warnings []sqlstate.Error
	warn := func(warning sqlstate.Error) { warnings = append(warnings, warning) }
	plan := GrantPlan{Changed: make(map[ids.ObjectAddress]ObjectGrantChange)}
	for _, target := range req.targets {
		address := target.Address()
		if err := ctx.Err(); err != nil {
			return GrantPlan{}, warnings, err
		}
		if target.Kind() != ids.RelationObject {
			d := target
			if prior := plan.Changed[address].Descriptor; prior != nil {
				d = prior
			}
			next, warning, err := changeNonRelationGrant(ctx, view, d, s, req.grantees, req.principals, req.superuser, req.tableMask, req.tableAll)
			if warning != nil {
				warn(*warning)
			}
			if err != nil {
				return GrantPlan{}, warnings, err
			}
			if next != nil {
				plan.Changed[address] = ObjectGrantChange{Descriptor: next}
				if plan.Candidate == nil {
					plan.Candidate = catalog.CloneState(view.Catalog.State())
				}
				if parameter, ok := next.(*catalog.Parameter); ok {
					catalog.InstallParameterGrant(plan.Candidate, parameter)
					if plan.Candidate.Get(address) == nil {
						plan.Changed[address] = ObjectGrantChange{}
					}
				} else {
					plan.Candidate.SetDescriptor(address, next)
				}
			}
			continue
		}
		relation := target.(*catalog.Relation)
		prior := plan.Changed[address]
		if prior.Descriptor != nil {
			relation = prior.Descriptor.(*catalog.Relation)
		}
		change, changed, err := relationGrantChange(ctx, view, s, req, relation, prior, warn)
		if err != nil {
			return GrantPlan{}, warnings, err
		}
		if !changed {
			continue
		}
		plan.Changed[address] = change
		next := change.Descriptor.(*catalog.Relation)
		if next.RelationKind == catalog.SequenceRelation {
			stamp, err := revision()
			if err != nil {
				return GrantPlan{}, warnings, err
			}
			next.SequencePlanStamp = stamp
		}
		if plan.Candidate == nil {
			plan.Candidate = catalog.CloneState(view.Catalog.State())
		}
		plan.Candidate.SetDescriptor(address, next)
	}
	return plan, warnings, nil
}

// relationGrantChange applies s to relation's table and column ACLs. prior is
// the change an earlier target of the statement made to the same relation.
// It returns false when neither ACL changes.
func relationGrantChange(ctx context.Context, view access.View, s *parser.ObjectGrant, req GrantRequest, relation *catalog.Relation, prior ObjectGrantChange, warn func(sqlstate.Error)) (ObjectGrantChange, bool, error) {
	if s.Kind == "sequence" && relation.RelationKind != catalog.SequenceRelation {
		return ObjectGrantChange{}, false, sqlError("42809", fmt.Sprintf(`"%s" is not a sequence`, relation.Name()))
	}
	switch relation.RelationKind {
	case catalog.IndexRelation:
		return ObjectGrantChange{}, false, sqlError("42809", fmt.Sprintf(`"%s" is an index`, relation.Name()))
	case catalog.CompositeRelation:
		return ObjectGrantChange{}, false, sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, relation.Name()))
	case catalog.CatalogRelation:
		return ObjectGrantChange{}, false, sqlError("0A000", "statement is not supported")
	}
	classMask := authorization.TableGrantPrivileges
	noun := "table"
	if relation.RelationKind == catalog.SequenceRelation {
		classMask = authorization.DefaultMask(authorization.DefaultSequences)
		noun = "sequence"
	}
	targetMask := req.tableMask
	if req.tableAll {
		targetMask = classMask
	} else if relation.RelationKind == catalog.SequenceRelation && s.Kind == "" {
		if targetMask & ^classMask != 0 {
			warn(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: fmt.Sprintf(`sequence "%s" only supports USAGE, SELECT, and UPDATE privileges`, relation.Name())})
		}
		targetMask &= classMask
	} else if targetMask & ^classMask != 0 {
		for _, item := range s.Privileges {
			if _, err := authorization.ObjectPrivilege(item, ""); err != nil {
				return ObjectGrantChange{}, false, err
			}
		}
	}
	originalTable := relation.Security.ACL
	nextTable := relation.Security.ACL
	var tableChanged bool
	var err error
	if targetMask != 0 {
		source, available, sourceErr := authorization.ObjectGrantSource(ctx, relation.Owner, originalTable, authorization.ACLValue{}, req.principals, req.superuser, targetMask)
		if sourceErr != nil {
			return ObjectGrantChange{}, false, sourceErr
		}
		if available == 0 {
			hasRelationPrivileges := authorization.ACLRights(originalTable, relation.Owner, req.principals, classMask, false) != 0 || access.PredefinedPrivileges(relation, req.principals) != 0
			if !hasRelationPrivileges {
				return ObjectGrantChange{}, false, sqlError("42501", "permission denied for "+noun+" "+relation.Name())
			}
		}
		if warning, ok := authorization.GrantWarning(relation.Name(), "", targetMask, available, req.tableAll, s.Revoke); ok {
			warn(warning)
		}
		if available != 0 {
			materialized := originalTable
			if !materialized.Explicit {
				if relation.RelationKind == catalog.SequenceRelation {
					materialized = authorization.DefaultBaseline(relation.Owner, authorization.DefaultSequences, false)
				} else {
					materialized = authorization.MaterializeRelationACL(originalTable, relation.Owner)
				}
			}
			nextTable, tableChanged, err = authorization.ChangeObjectACL(ctx, view.Roles, materialized, relation.Owner, source, req.grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
		}
		if err != nil {
			return ObjectGrantChange{}, false, err
		}
		tableChanged = tableChanged || available != 0 && !relation.Security.ACL.Explicit
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
		if relation.RelationKind != catalog.ViewRelation {
			for i, name := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid"} {
				addColumn(int32(-6+i), name, targetMask&authorization.ColumnGrantPrivileges)
			}
		}
		for i, column := range relation.Columns {
			addColumn(relation.Attributes[i], column.Name, targetMask&authorization.ColumnGrantPrivileges)
		}
	}
	for i, item := range s.Privileges {
		mask := req.masks[i]
		if len(item.Columns) != 0 && relation.RelationKind == catalog.SequenceRelation {
			if mask & ^authorization.SelectPrivilege != 0 {
				warn(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: fmt.Sprintf(`sequence "%s" only supports SELECT column privileges`, relation.Name())})
			}
			mask &= authorization.SelectPrivilege
		}
		for _, column := range item.Columns {
			index, ok := relation.Index[column.Text]
			if !ok {
				return ObjectGrantChange{}, false, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column.Text, relation.Name()))
			}
			if mask != 0 {
				addColumn(relation.Attributes[index], column.Text, mask)
			}
		}
	}
	// Release traversal uses signed stable attributes, after merging all
	// textual requests (including implicit table-revoke column masks).
	slices.SortFunc(columns, func(a, b columnGrantRequest) int { return cmp.Compare(a.sub, b.sub) })
	var nextColumns map[int32]authorization.ACLValue
	var hasColumnPrivileges, columnPrivilegesChecked bool
	for _, column := range columns {
		old := relation.Security.ColumnACL[column.sub]
		source, available, sourceErr := authorization.ObjectGrantSource(ctx, relation.Owner, originalTable, old, req.principals, req.superuser, column.mask)
		if sourceErr != nil {
			return ObjectGrantChange{}, false, sourceErr
		}
		if available == 0 {
			if !columnPrivilegesChecked {
				hasColumnPrivileges = authorization.ACLRights(originalTable, relation.Owner, req.principals, authorization.ColumnGrantPrivileges, false)&authorization.ColumnGrantPrivileges != 0 ||
					access.PredefinedPrivileges(relation, req.principals)&authorization.ColumnGrantPrivileges != 0
				columnPrivilegesChecked = true
			}
			if !hasColumnPrivileges && authorization.ACLRights(old, authorization.PrincipalRef{}, req.principals, authorization.ColumnGrantPrivileges, false)&authorization.ColumnGrantPrivileges == 0 {
				return ObjectGrantChange{}, false, sqlError("42501", fmt.Sprintf(`permission denied for column "%s" of relation "%s"`, column.name, relation.Name()))
			}
		}
		if warning, ok := authorization.GrantWarning(relation.Name(), column.name, column.mask, available, column.mask == authorization.ColumnGrantPrivileges, s.Revoke); ok {
			warn(warning)
		}
		// System attributes participate in release diagnostics, not native storage.
		if column.sub <= 0 {
			continue
		}
		next, didChange, err := authorization.ChangeObjectACL(ctx, view.Roles, old, relation.Owner, source, req.grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
		if err != nil {
			return ObjectGrantChange{}, false, err
		}
		if didChange {
			if nextColumns == nil {
				if prior.columnsOwned {
					nextColumns = relation.Security.ColumnACL
				} else {
					nextColumns = make(map[int32]authorization.ACLValue, len(relation.Security.ColumnACL)+1)
					for sub, acl := range relation.Security.ColumnACL {
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
	if !tableChanged && nextColumns == nil {
		return ObjectGrantChange{}, false, nil
	}
	next, _ := prior.Descriptor.(*catalog.Relation)
	if next == nil {
		copy := *relation
		next = &copy
	}
	change := ObjectGrantChange{Descriptor: next, columnsOwned: prior.columnsOwned || nextColumns != nil}
	next.Security.ACL = nextTable
	if nextColumns != nil {
		next.Security.ColumnACL = nextColumns
	}
	if err := access.ValidateACL(view.Roles, next); err != nil {
		return ObjectGrantChange{}, false, err
	}
	return change, true, nil
}
