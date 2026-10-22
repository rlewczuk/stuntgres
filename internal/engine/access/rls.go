package access

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// AdjacentRLSDisposition is used only by the MERGE/ON CONFLICT action gate.
// Ordinary commands retain selected expression groups and evaluate actual rows.
type AdjacentRLSDisposition uint8

const (
	AdjacentRLSUnrestricted AdjacentRLSDisposition = iota
	AdjacentRLSDefaultDeny
	AdjacentRLSNeedsExpressions
)

// RowSecurityApplies is metadata only, independent of policy contents.
func RowSecurityApplies(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef) (bool, error) {
	if !d.Security.RLSEnabled {
		return false, nil
	}
	actor, err := roles.Descriptor(principal)
	if err != nil {
		return false, err
	}
	if actor.Superuser || actor.BypassRLS {
		return false, nil
	}
	if !d.Security.RLSForced {
		owner, err := roles.InheritsRole(principal, d.Owner)
		if err != nil {
			return false, err
		}
		if owner {
			return false, nil
		}
	}
	return true, nil
}

// RowSecuritySettingGuard rejects queries whose logical result would depend on
// RLS when the executing session disables policy application. It never bypasses
// policies and deliberately leaves the metadata-only applicability API intact.
func RowSecuritySettingGuard(enabled bool, roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef) error {
	if enabled {
		return nil
	}
	applies, err := RowSecurityApplies(roles, d, principal)
	if err != nil || !applies {
		return err
	}
	owner, err := roles.InheritsRole(principal, d.Owner)
	if err != nil {
		return err
	}
	diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`query would be affected by row-level security policy for table "%s"`, d.Name())}
	if owner {
		diagnostic.Hint = "To disable the policy for the table's owner, use ALTER TABLE NO FORCE ROW LEVEL SECURITY."
	}
	return diagnostic
}

// AdjacentRowSecurityGroups summarizes selected groups only for unsupported
// action-specific MERGE/ON CONFLICT execution. It is never an ordinary row filter.
func AdjacentRowSecurityGroups(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed, selectRead bool) (AdjacentRLSDisposition, error) {
	selection, err := SelectRowSecurity(roles, d, principal, command, proposed, selectRead)
	if err != nil {
		return AdjacentRLSUnrestricted, err
	}
	if selection.Denied() {
		return AdjacentRLSDefaultDeny, nil
	}
	if selection.Count != 0 {
		return AdjacentRLSNeedsExpressions, nil
	}
	return AdjacentRLSUnrestricted, nil
}

func AdjacentRowSecurityExpressionError() error {
	return sqlError("0A000", "row-security policy expression enforcement is unsupported")
}

func AdjacentRowSecurityProposal(decision AdjacentRLSDisposition, table string) error {
	switch decision {
	case AdjacentRLSDefaultDeny:
		return sqlError("42501", fmt.Sprintf(`new row violates row-level security policy for table "%s"`, table))
	case AdjacentRLSNeedsExpressions:
		return AdjacentRowSecurityExpressionError()
	default:
		return nil
	}
}

// RowSecurityLayer is one view layer of an auto-updatable view write, from
// the outermost view inward: the view's owner and whether it runs with
// security_invoker.
type RowSecurityLayer struct {
	Owner   authorization.PrincipalRef
	Invoker bool
}

// RowSecurityPrincipal returns the role whose policies apply to the base
// relation: the innermost view's owner, or current for an invoker view or a
// direct write.
func RowSecurityPrincipal(current authorization.PrincipalRef, layers []RowSecurityLayer) authorization.PrincipalRef {
	principal := current
	for _, layer := range layers {
		principal = layer.Owner
		if layer.Invoker {
			principal = current
		}
	}
	return principal
}
