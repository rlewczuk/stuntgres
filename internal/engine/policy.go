package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

type policyRole struct {
	Public bool
	Ref    authorization.PrincipalRef
}

type policyExpression struct {
	syntax       parser.Expr
	expression   expr.Expr
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
	OID          ids.OID
	Name         string
	Permissive   bool
	Command      policyCommand
	Roles        []policyRole
	Using, Check policyPayload
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
		return []policyRole{{Public: true}}, false, nil
	}
	roles := make([]policyRole, 0, len(raw))
	for _, role := range raw {
		if role.Public {
			return []policyRole{{Public: true}}, len(raw) > 1, nil
		}
		if role.Role.Name == "none" {
			return nil, false, sqlError("42939", `role name "none" is reserved`)
		}
		ref, err := authorization.MembershipRole(auth.roles, auth.sessionRoleState, role.Role)
		if err != nil {
			return nil, false, err
		}
		roles = append(roles, policyRole{Ref: ref})
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
	return slices.IndexFunc(d.Policies, func(p policyDefinition) bool { return p.Name == name })
}

func policyMissing(name string, d *relationDescriptor) error {
	return sqlError("42704", fmt.Sprintf(`policy "%s" for table "%s" does not exist`, name, d.name))
}

func policyDuplicate(name string, d *relationDescriptor) error {
	return sqlError("42710", fmt.Sprintf(`policy "%s" for table "%s" already exists`, name, d.name))
}

func validatePolicies(view CatalogRead, d *relationDescriptor) error {
	if len(d.Policies) != 0 && d.RelationKind != tableRelation && d.RelationKind != partitionedRelation {
		return fmt.Errorf("stuntgres: policies require a table")
	}
	for i, p := range d.Policies {
		if p.OID == 0 || p.Name == "" || len(p.Name) > 63 || !utf8.ValidString(p.Name) || strings.IndexByte(p.Name, 0) >= 0 || i > 0 && d.Policies[i-1].Name >= p.Name || len(p.Roles) == 0 {
			return fmt.Errorf("stuntgres: invalid policy identity")
		}
		switch p.Command {
		case policyAll, policySelect, policyInsert, policyUpdate, policyDelete:
		default:
			return fmt.Errorf("stuntgres: invalid policy command")
		}
		if p.Command == policyInsert && p.Using != nil || (p.Command == policySelect || p.Command == policyDelete) && p.Check != nil {
			return fmt.Errorf("stuntgres: invalid policy clauses")
		}
		for _, condition := range []policyPayload{p.Using, p.Check} {
			if condition != nil {
				if err := condition.Validate(view, d); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
