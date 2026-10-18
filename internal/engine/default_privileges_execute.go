package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
	grantees := make([]authorization.ACLGrantee, len(s.Grantees))
	publicGrantOption := false
	for i, spec := range s.Grantees {
		if spec.Public {
			grantees[i] = authorization.ACLGrantee{Public: true}
			publicGrantOption = s.GrantOption
			continue
		}
		role, err := authorization.MembershipRole(v.roles, v.state, spec.Role)
		if err != nil {
			return Result{}, err
		}
		grantees[i] = authorization.ACLGrantee{Role: role}
	}
	class := authorization.DefaultClass(s.Class)
	if class == 0 {
		return Result{}, fmt.Errorf("stuntgres: invalid default ACL class")
	}
	var mask authorization.Privileges
	for _, item := range s.Privileges {
		bit, err := authorization.DefaultPrivilege(item, class)
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
		creator, err := authorization.MembershipRole(v.roles, v.state, spec)
		if err != nil {
			return Result{}, err
		}
		allowed, err := v.roles.InheritsRole(v.state.CurrentUser, creator)
		if err != nil {
			return Result{}, err
		}
		if !allowed {
			return Result{}, sqlError("42501", "permission denied to change default privileges")
		}
		// Match PostgreSQL's resolve-and-act order without publishing any of
		// these private updates until every creator/schema action succeeds.
		for i := range schemaCount {
			var schema ids.ObjectAddress
			if schemas != nil {
				name := schemas[i].Text
				schema = v.catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
				if schema == (ids.ObjectAddress{}) {
					return Result{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
				}
				if class == authorization.DefaultSchemas || class == authorization.DefaultLargeObjects {
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
			var acl authorization.ACLValue
			if old != nil {
				acl = old.acl
			} else {
				acl = authorization.DefaultBaseline(creator, class, schema != (ids.ObjectAddress{}))
			}
			next, changed, err := authorization.ChangeObjectACL(op.Context(), v.roles, acl, creator, creator, grantees, mask, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			if err != nil {
				return Result{}, err
			}
			if !changed {
				continue
			}
			if updates == nil {
				updates = make(map[defaultACLKey]*defaultACLRecord)
			}
			if authorization.MatchesDefaultBaseline(next, creator, class, schema != (ids.ObjectAddress{})) {
				updates[key] = nil
			} else {
				updates[key] = &defaultACLRecord{acl: next}
			}
		}
	}
	for key, record := range updates {
		old := v.catalog.state.defaultACLs[key]
		if old == record || old != nil && record != nil && authorization.EqualACL(old.acl, record.acl) {
			delete(updates, key)
		}
	}
	if len(updates) == 0 {
		return Result{}, op.Context().Err()
	}
	candidate := *v.catalog.state
	candidate.defaultACLs = overlayDefaultACLs(v.catalog.state.defaultACLs, updates)
	candidateCatalog := e.catalog.read(&candidate)
	candidateRoles := e.roleRead(candidateCatalog, v.roleRecord)
	if err := validateDefaultACLs(candidateRoles, candidateCatalog); err != nil {
		return Result{}, err
	}
	if v.roleRecord.revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	nextRoles := advanceRoleRevision(v.roleRecord)
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if op.session.transactions.retainsTransaction() {
		changes := cloneCatalogChanges(op.session.transactions.catalog)
		// Default utilities use the selected private DB authority, but their
		// retained effects must fence its exact committed predecessor.
		if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ids.ObjectRef{}, nil), changes); err != nil {
			return Result{}, err
		}
		for key, record := range updates {
			e.defaultACLChangeLocked(changes, key, record)
		}
		op.session.transactions.catalog = changes
	} else {
		e.catalog.state = &candidate
	}
	e.installOperationRolesLocked(op, nextRoles)
	return Result{}, nil
}
