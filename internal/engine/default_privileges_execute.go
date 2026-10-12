package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) alterDefaultPrivilegesLocked(op *Operation, v AuthorizationView, s *parser.AlterDefaultPrivileges) (Result, error) {
	var creators []parser.RoleSpec
	var schemas []parser.Identifier
	for _, option := range s.Options {
		duplicate := false
		if option.Roles != nil {
			duplicate = creators != nil
			creators = option.Roles
		} else {
			duplicate = schemas != nil
			schemas = option.Schemas
		}
		if duplicate {
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(option.Pos.Offset + 1)}
		}
	}
	grantees := make([]ACLGrantee, len(s.Grantees))
	publicGrantOption := false
	for i, spec := range s.Grantees {
		if spec.Public {
			grantees[i] = ACLGrantee{Public: true}
			publicGrantOption = s.GrantOption
			continue
		}
		role, err := membershipRole(v.roles, v.state, spec.Role)
		if err != nil {
			return Result{}, err
		}
		grantees[i] = ACLGrantee{Role: role}
	}
	class := defaultClass(s.Class)
	if class == 0 {
		return Result{}, fmt.Errorf("stuntgres: invalid default ACL class")
	}
	var mask Privileges
	for _, item := range s.Privileges {
		bit, err := defaultPrivilege(item, class)
		if err != nil {
			return Result{}, err
		}
		mask |= bit
	}
	if creators == nil {
		creators = []parser.RoleSpec{{Identity: "current"}}
	}
	schemaCount := 1
	if schemas != nil {
		schemaCount = len(schemas)
	}
	var updates map[defaultACLKey]*defaultACLRecord
	for _, spec := range creators {
		creator, err := membershipRole(v.roles, v.state, spec)
		if err != nil {
			return Result{}, err
		}
		allowed, err := v.roles.inheritsRole(v.state.CurrentUser, creator)
		if err != nil {
			return Result{}, err
		}
		if !allowed {
			return Result{}, sqlError("42501", "permission denied to change default privileges")
		}
		// Match PostgreSQL's resolve-and-act order without publishing any of
		// these private updates until every creator/schema action succeeds.
		for i := range schemaCount {
			var schema ObjectAddress
			if schemas != nil {
				name := schemas[i].Text
				schema = v.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name}]
				if schema == (ObjectAddress{}) {
					return Result{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
				}
				if class == defaultSchemas || class == defaultLargeObjects {
					return Result{}, sqlError("0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON "+strings.ToUpper(s.Class))
				}
			}
			if publicGrantOption {
				return Result{}, sqlError("0LP01", "grant options can only be granted to roles")
			}
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			key := defaultACLKey{creator: creator, schema: schema, class: class}
			old, exists := updates[key]
			if !exists {
				old = v.catalog.state.defaultACLs[key]
			}
			var acl ACLValue
			if old != nil {
				acl = old.acl
			} else {
				acl = defaultBaseline(creator, class, schema != (ObjectAddress{}))
			}
			next, changed, err := changeObjectACL(op.Context(), v.roles, acl, creator, creator, grantees, mask, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			if err != nil {
				return Result{}, err
			}
			if !changed {
				continue
			}
			if updates == nil {
				updates = make(map[defaultACLKey]*defaultACLRecord)
			}
			if matchesDefaultBaseline(next, creator, class, schema != (ObjectAddress{})) {
				updates[key] = nil
			} else {
				updates[key] = &defaultACLRecord{acl: next}
			}
		}
	}
	for key, record := range updates {
		old := v.catalog.state.defaultACLs[key]
		if old == record || old != nil && record != nil && equalACL(old.acl, record.acl) {
			delete(updates, key)
		}
	}
	if len(updates) == 0 {
		return Result{}, op.Context().Err()
	}
	candidate := *v.catalog.state
	candidate.defaultACLs = overlayDefaultACLs(v.catalog.state.defaultACLs, updates)
	candidateRoles := v.roles
	candidateRoles.catalog = CatalogRead{e, &candidate}
	if err := validateDefaultACLs(candidateRoles, candidateRoles.catalog); err != nil {
		return Result{}, err
	}
	if v.roles.state.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	nextRoles := advanceRoleRevision(v.roles.state)
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if op.session.transactions.retainedMessage {
		changes := cloneCatalogChanges(op.session.transactions.catalog)
		// Default utilities use the selected private DB authority, but their
		// retained effects must fence its exact committed predecessor.
		if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ObjectRef{}, nil), changes); err != nil {
			return Result{}, err
		}
		for key, record := range updates {
			e.defaultACLChangeLocked(changes, key, record)
		}
		op.session.transactions.catalog = changes
	} else {
		e.state = &candidate
	}
	e.installOperationRolesLocked(op, nextRoles)
	return Result{}, nil
}
