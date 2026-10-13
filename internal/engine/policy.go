package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

type policyRole struct {
	public bool
	ref    authorization.PrincipalRef
}

type policyExpression struct {
	syntax       parser.Expr
	expression   BoundExpr
	presentation *viewDefinition
}

type policyCommand string

const (
	policyAll    policyCommand = "all"
	policySelect policyCommand = "select"
	policyInsert policyCommand = "insert"
	policyUpdate policyCommand = "update"
	policyDelete policyCommand = "delete"
)

type policyDefinition struct {
	OID          OID
	Name         string
	permissive   bool
	command      policyCommand
	roles        []policyRole
	using, check *policyExpression
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

func policyClauses(command policyCommand, using, check parser.Expr, creating bool) error {
	if command == policyInsert && using != nil {
		return sqlError("42601", "only WITH CHECK expression allowed for INSERT")
	}
	if (command == policySelect || command == policyDelete) && check != nil {
		if creating {
			return sqlError("42601", "WITH CHECK cannot be applied to SELECT or DELETE")
		}
		return sqlError("42601", "only USING expression allowed for SELECT, DELETE")
	}
	return nil
}

func resolvePolicyRoles(auth AuthorizationView, raw []parser.GrantGrantee) ([]policyRole, bool, error) {
	if raw == nil {
		return []policyRole{{public: true}}, false, nil
	}
	roles := make([]policyRole, 0, len(raw))
	for _, role := range raw {
		if role.Public {
			return []policyRole{{public: true}}, len(raw) > 1, nil
		}
		if role.Role.Name == "none" {
			return nil, false, sqlError("42939", `role name "none" is reserved`)
		}
		ref, err := authorization.MembershipRole(auth.roles, auth.sessionRoleState, role.Role)
		if err != nil {
			return nil, false, err
		}
		roles = append(roles, policyRole{ref: ref})
	}
	return roles, false, nil
}

func policyPreflight(op *Operation, auth AuthorizationView, stmt parser.Statement, notices bool) error {
	var roles []parser.GrantGrantee
	switch s := stmt.(type) {
	case *parser.CreatePolicy:
		if err := policyClauses(policyCommand(s.Command), s.Using, s.Check, true); err != nil {
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

func policyIndex(d *relationDescriptor, name string) int {
	return slices.IndexFunc(d.policies, func(p policyDefinition) bool { return p.Name == name })
}

func policyMissing(name string, d *relationDescriptor) error {
	return sqlError("42704", fmt.Sprintf(`policy "%s" for table "%s" does not exist`, name, d.name))
}

func policyDuplicate(name string, d *relationDescriptor) error {
	return sqlError("42710", fmt.Sprintf(`policy "%s" for table "%s" already exists`, name, d.name))
}

func validatePolicies(view CatalogRead, d *relationDescriptor) error {
	if len(d.policies) != 0 && d.relationKind != tableRelation && d.relationKind != partitionedRelation {
		return fmt.Errorf("stuntgres: policies require a table")
	}
	for i, p := range d.policies {
		if p.OID == 0 || p.Name == "" || len(p.Name) > 63 || !utf8.ValidString(p.Name) || strings.IndexByte(p.Name, 0) >= 0 || i > 0 && d.policies[i-1].Name >= p.Name || len(p.roles) == 0 {
			return fmt.Errorf("stuntgres: invalid policy identity")
		}
		switch p.command {
		case policyAll, policySelect, policyInsert, policyUpdate, policyDelete:
		default:
			return fmt.Errorf("stuntgres: invalid policy command")
		}
		if p.command == policyInsert && p.using != nil || (p.command == policySelect || p.command == policyDelete) && p.check != nil {
			return fmt.Errorf("stuntgres: invalid policy clauses")
		}
		for _, expr := range []*policyExpression{p.using, p.check} {
			if expr != nil {
				if err := validatePolicyExpression(view, d, expr); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
