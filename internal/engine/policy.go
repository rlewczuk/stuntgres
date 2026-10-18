package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

type policyExpression struct {
	syntax       parser.Expr
	expression   expr.Expr
	presentation *viewDefinition
}

func policyOwnerNoun(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.CreatePolicy, *parser.AlterPolicy:
		return "table"
	case *parser.DropPolicy:
		return "relation"
	default:
		return ""
	}
}

func policyClauses(command catalog.PolicyCommand, using, check parser.Expr, creating bool) error {
	if command == catalog.PolicyInsert && using != nil {
		return sqlError("42601", "only WITH CHECK expression allowed for INSERT")
	}
	if (command == catalog.PolicySelect || command == catalog.PolicyDelete) && check != nil {
		if creating {
			return sqlError("42601", "WITH CHECK cannot be applied to SELECT or DELETE")
		}
		return sqlError("42601", "only USING expression allowed for SELECT, DELETE")
	}
	return nil
}

func resolvePolicyRoles(auth AuthorizationView, raw []parser.GrantGrantee) ([]catalog.PolicyRole, bool, error) {
	if raw == nil {
		return []catalog.PolicyRole{{Public: true}}, false, nil
	}
	roles := make([]catalog.PolicyRole, 0, len(raw))
	for _, role := range raw {
		if role.Public {
			return []catalog.PolicyRole{{Public: true}}, len(raw) > 1, nil
		}
		if role.Role.Name == "none" {
			return nil, false, sqlError("42939", `role name "none" is reserved`)
		}
		ref, err := authorization.MembershipRole(auth.roles, auth.sessionRoleState, role.Role)
		if err != nil {
			return nil, false, err
		}
		roles = append(roles, catalog.PolicyRole{Ref: ref})
	}
	return roles, false, nil
}

func policyPreflight(op *Operation, auth AuthorizationView, stmt parser.Statement, notices bool) error {
	var roles []parser.GrantGrantee
	switch s := stmt.(type) {
	case *parser.CreatePolicy:
		if err := policyClauses(catalog.PolicyCommand(s.Command), s.Using, s.Check, true); err != nil {
			return err
		}
		roles = s.Roles
	case *parser.AlterPolicy:
		if s.Rename != nil {
			return nil
		}
		roles = s.Roles
	default:
		return nil
	}
	_, warn, err := resolvePolicyRoles(auth, roles)
	if err == nil && warn && notices {
		op.emitNotice(levelWarning, sqlstate.Error{
			Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023",
			Message: "ignoring specified roles other than PUBLIC",
			Hint:    "All roles are members of the PUBLIC role.",
		})
	}
	return err
}

func policyIndex(d *catalog.Relation, name string) int {
	return slices.IndexFunc(d.Policies, func(p catalog.PolicyDefinition) bool { return p.Name == name })
}

func policyMissing(name string, d *catalog.Relation) error {
	return sqlError("42704", fmt.Sprintf(`policy "%s" for table "%s" does not exist`, name, d.Name()))
}

func policyDuplicate(name string, d *catalog.Relation) error {
	return sqlError("42710", fmt.Sprintf(`policy "%s" for table "%s" already exists`, name, d.Name()))
}

func validatePolicies(view CatalogRead, d *catalog.Relation) error {
	if len(d.Policies) != 0 && d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation {
		return fmt.Errorf("stuntgres: policies require a table")
	}
	for i, p := range d.Policies {
		if p.OID == 0 || p.Name == "" || len(p.Name) > 63 || !utf8.ValidString(p.Name) || strings.IndexByte(p.Name, 0) >= 0 || i > 0 && d.Policies[i-1].Name >= p.Name || len(p.Roles) == 0 {
			return fmt.Errorf("stuntgres: invalid policy identity")
		}
		switch p.Command {
		case catalog.PolicyAll, catalog.PolicySelect, catalog.PolicyInsert, catalog.PolicyUpdate, catalog.PolicyDelete:
		default:
			return fmt.Errorf("stuntgres: invalid policy command")
		}
		if p.Command == catalog.PolicyInsert && p.Using != nil || (p.Command == catalog.PolicySelect || p.Command == catalog.PolicyDelete) && p.Check != nil {
			return fmt.Errorf("stuntgres: invalid policy clauses")
		}
		for _, condition := range []catalog.PolicyPayload{p.Using, p.Check} {
			if condition != nil {
				if err := condition.Validate(view, d); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
