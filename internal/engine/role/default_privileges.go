package role

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DefaultPrivilegesPlan validates ALTER DEFAULT PRIVILEGES and returns the
// default ACL records it changes. A nil record restores the key's baseline.
// The records carry no OID yet; keys whose record would not change are left
// out.
func DefaultPrivilegesPlan(ctx context.Context, view access.View, s *parser.AlterDefaultPrivileges) (map[catalog.DefaultACLKey]*catalog.DefaultACLRecord, error) {
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
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(option.Pos.Offset + 1)}
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
		role, err := authorization.MembershipRole(view.Roles, view.State, spec.Role)
		if err != nil {
			return nil, err
		}
		grantees[i] = authorization.ACLGrantee{Role: role}
	}
	class := authorization.DefaultClass(s.Class)
	if class == 0 {
		return nil, fmt.Errorf("stuntgres: invalid default ACL class")
	}
	var mask authorization.Privileges
	for _, item := range s.Privileges {
		bit, err := authorization.DefaultPrivilege(item, class)
		if err != nil {
			return nil, err
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
	var updates map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	for _, spec := range creators {
		creator, err := authorization.MembershipRole(view.Roles, view.State, spec)
		if err != nil {
			return nil, err
		}
		allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, creator)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, sqlError("42501", "permission denied to change default privileges")
		}
		// Match PostgreSQL's resolve-and-act order without publishing any of
		// these private updates until every creator/schema action succeeds.
		for i := range schemaCount {
			var schema ids.ObjectAddress
			if schemas != nil {
				name := schemas[i].Text
				schema = view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
				if schema == (ids.ObjectAddress{}) {
					return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
				}
				if class == authorization.DefaultSchemas || class == authorization.DefaultLargeObjects {
					return nil, sqlError("0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON "+strings.ToUpper(s.Class))
				}
			}
			if publicGrantOption {
				return nil, sqlError("0LP01", "grant options can only be granted to roles")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key := catalog.DefaultACLKey{Creator: creator, Schema: schema, Class: class}
			old, exists := updates[key]
			if !exists {
				old = view.Catalog.State().DefaultACL(key)
			}
			var acl authorization.ACLValue
			if old != nil {
				acl = old.ACL
			} else {
				acl = authorization.DefaultBaseline(creator, class, schema != (ids.ObjectAddress{}))
			}
			next, changed, err := authorization.ChangeObjectACL(ctx, view.Roles, acl, creator, creator, grantees, mask, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
			if err != nil {
				return nil, err
			}
			if !changed {
				continue
			}
			if updates == nil {
				updates = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
			}
			if authorization.MatchesDefaultBaseline(next, creator, class, schema != (ids.ObjectAddress{})) {
				updates[key] = nil
			} else {
				updates[key] = &catalog.DefaultACLRecord{ACL: next}
			}
		}
	}
	for key, record := range updates {
		old := view.Catalog.State().DefaultACL(key)
		if old == record || old != nil && record != nil && authorization.EqualACL(old.ACL, record.ACL) {
			delete(updates, key)
		}
	}
	return updates, nil
}
