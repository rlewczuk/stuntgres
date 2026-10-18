package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Empty protected inputs still own their immutable visibility catalog. Do not
// capture physical prefixes or permit later live-version selection to revive rows.
func captureDeniedQuerySource(view *VisibilityView, ref ids.ObjectRef) (*querySource, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, fmt.Errorf("stuntgres: visibility view is released")
	}
	data := pin.read()
	if data.catalog.owner == nil || ref.Domain() != data.catalog.owner.Domain || ref.Epoch != data.catalog.Epoch() || data.catalog.Get(ref.Address) == nil {
		pin.Release()
		return nil, fmt.Errorf("stuntgres: invalid visibility target")
	}
	return &querySource{view: pin, ref: ref, rlsDenied: true}, nil
}

type rlsDisposition uint8

const (
	rlsUnrestricted rlsDisposition = iota
	rlsDefaultDeny
	rlsNeedsExpressions
)

func rowSecurityAction(action parser.AlterTableActionKind) bool {
	return action >= parser.EnableRowSecurity && action <= parser.NoForceRowSecurity
}

func rowSecurityActions(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if !rowSecurityAction(action.Action) {
			return false
		}
	}
	return true
}

func rowSecurityFlags(old *relationDescriptor, actions []parser.AlterTableAction) *relationDescriptor {
	enabled, forced := old.RLSEnabled, old.RLSForced
	for _, action := range actions {
		switch action.Action {
		case parser.EnableRowSecurity:
			enabled = true
		case parser.DisableRowSecurity:
			enabled = false
		case parser.ForceRowSecurity:
			forced = true
		case parser.NoForceRowSecurity:
			forced = false
		}
	}
	if enabled == old.RLSEnabled && forced == old.RLSForced {
		return old
	}
	next := *old
	next.RLSEnabled, next.RLSForced = enabled, forced
	return &next
}

func rowSecurityActionLabel(action parser.AlterTableActionKind) string {
	switch action {
	case parser.EnableRowSecurity:
		return "ENABLE ROW SECURITY"
	case parser.DisableRowSecurity:
		return "DISABLE ROW SECURITY"
	case parser.ForceRowSecurity:
		return "FORCE ROW SECURITY"
	default:
		return "NO FORCE ROW SECURITY"
	}
}

func rowSecurityKind(d *relationDescriptor, action parser.AlterTableActionKind) error {
	if d.RelationKind == tableRelation || d.RelationKind == partitionedRelation {
		return nil
	}
	kind := ""
	switch d.RelationKind {
	case viewRelation:
		kind = "views"
	case materializedViewRelation:
		kind = "materialized views"
	case sequenceRelation:
		kind = "sequences"
	case indexRelation:
		kind = "indexes"
	case compositeRelation:
		kind = "composite types"
	default:
		return tableKind(d, rowSecurityActionLabel(action))
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, rowSecurityActionLabel(action), d.name), Detail: "This operation is not supported for " + kind + "."}
}

// Applicability is metadata only. Even TRUE/FALSE/NULL syntax requires the
// adjacent expression-enforcement capability; nil clauses contribute nothing.
func rowSecurityApplies(auth AuthorizationView, d *relationDescriptor, principal authorization.PrincipalRef) (bool, error) {
	if !d.RLSEnabled {
		return false, nil
	}
	actor, err := auth.roles.Descriptor(principal)
	if err != nil {
		return false, err
	}
	if actor.Superuser || actor.BypassRLS {
		return false, nil
	}
	if !d.RLSForced {
		owner, err := auth.roles.InheritsRole(principal, d.Owner)
		if err != nil {
			return false, err
		}
		if owner {
			return false, nil
		}
	}
	return true, nil
}

func rowSecurityDecision(auth AuthorizationView, d *relationDescriptor, principal authorization.PrincipalRef, command policyCommand, proposed bool) (rlsDisposition, error) {
	applies, err := rowSecurityApplies(auth, d, principal)
	if err != nil || !applies {
		return rlsUnrestricted, err
	}
	for _, policy := range d.Policies {
		if !policy.Permissive || policy.Command != policyAll && policy.Command != command {
			continue
		}
		expression := policyExpressionOf(policy.Using)
		if proposed && command != policySelect {
			expression = policyExpressionOf(policy.Check)
			if expression == nil && (policy.Command == policyAll || policy.Command == policyUpdate) {
				expression = policyExpressionOf(policy.Using)
			}
		}
		if expression == nil {
			continue
		}
		for _, role := range policy.Roles {
			if role.Public {
				return rlsNeedsExpressions, nil
			}
			applies, err := auth.roles.InheritsRole(principal, role.Ref)
			if err != nil {
				return rlsUnrestricted, err
			}
			if applies {
				return rlsNeedsExpressions, nil
			}
		}
	}
	return rlsDefaultDeny, nil
}

func rowSecurityGroups(auth AuthorizationView, d *relationDescriptor, principal authorization.PrincipalRef, command policyCommand, proposed, selectRead bool) (rlsDisposition, error) {
	decision, err := rowSecurityDecision(auth, d, principal, command, proposed)
	if err != nil || !selectRead || command == policySelect {
		return decision, err
	}
	read, err := rowSecurityDecision(auth, d, principal, policySelect, proposed)
	if err != nil {
		return rlsUnrestricted, err
	}
	if decision == rlsDefaultDeny || read == rlsDefaultDeny {
		return rlsDefaultDeny, nil
	}
	if decision == rlsNeedsExpressions || read == rlsNeedsExpressions {
		return rlsNeedsExpressions, nil
	}
	return rlsUnrestricted, nil
}

func rowSecurityExpressionError() error {
	return sqlError("0A000", "row-security policy expression enforcement is unsupported")
}

func rowSecurityProposal(decision rlsDisposition, table string) error {
	switch decision {
	case rlsDefaultDeny:
		return sqlError("42501", fmt.Sprintf(`new row violates row-level security policy for table "%s"`, table))
	case rlsNeedsExpressions:
		return rowSecurityExpressionError()
	default:
		return nil
	}
}

func rowSecurityPrincipal(auth AuthorizationView, b binding) authorization.PrincipalRef {
	principal := auth.state.CurrentUser
	if b.viewWrite != nil {
		for _, layer := range b.viewWrite.layers {
			current := auth.catalog.MustRelation(layer.ref.Address)
			principal = current.Owner
			if viewOf(current).invoker {
				principal = auth.state.CurrentUser
			}
		}
	}
	return principal
}

// The caller holds Engine.mu; this never captures row data or mutates identity.
func (e *Engine) rowSecurityInputLocked(scope *StatementScope, d *relationDescriptor, principal authorization.PrincipalRef, command policyCommand, selectRead bool) (bool, error) {
	if !d.RLSEnabled {
		return false, nil
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return false, err
	}
	auth.catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
	if principal == (authorization.PrincipalRef{}) {
		principal = auth.state.CurrentUser
	}
	decision, err := rowSecurityGroups(auth, d, principal, command, false, selectRead)
	if err != nil {
		return false, err
	}
	if decision == rlsNeedsExpressions {
		return false, rowSecurityExpressionError()
	}
	return decision == rlsDefaultDeny, nil
}

func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b binding, stmt parser.Statement) error {
	scope.rlsTargetDenied = false
	scope.rlsProposal, scope.rlsMergeInsert, scope.rlsMergeUpdate = rlsUnrestricted, rlsUnrestricted, rlsUnrestricted
	if b.relation == nil {
		return nil
	}
	command := policyCommand("")
	switch stmt.(type) {
	case *parser.Insert:
		command = policyInsert
	case *parser.Update:
		command = policyUpdate
	case *parser.Delete:
		command = policyDelete
	case *parser.Merge:
		command = policySelect
	default:
		return nil
	}
	scope.rlsProposalTable = b.relation.name
	if !b.relation.RLSEnabled {
		return nil
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return err
	}
	if scope.ruleActionAuth != nil {
		auth.state.CurrentUser = scope.ruleActionAuth.state.CurrentUser
	}
	auth.catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
	principal := rowSecurityPrincipal(auth, b)
	reads := b.returning != nil || len(b.usedColumns) != 0
	if b.viewWrite != nil {
		reads = reads || len(b.viewWrite.logical.usedColumns) != 0
	}
	if command != policyInsert {
		decision, err := rowSecurityGroups(auth, b.relation, principal, command, false, reads)
		if err != nil {
			return err
		}
		if decision == rlsNeedsExpressions {
			return rowSecurityExpressionError()
		}
		scope.rlsTargetDenied = decision == rlsDefaultDeny
	}
	proposedRead := b.conflict != nil && len(b.conflict.reads) != 0
	switch stmt.(type) {
	case *parser.Insert:
		scope.rlsProposal, err = rowSecurityGroups(auth, b.relation, principal, policyInsert, true, proposedRead)
	case *parser.Update:
		scope.rlsProposal, err = rowSecurityGroups(auth, b.relation, principal, policyUpdate, true, reads)
	case *parser.Merge:
		scope.rlsMergeInsert, err = rowSecurityGroups(auth, b.relation, principal, policyInsert, true, false)
		if err == nil {
			scope.rlsMergeUpdate, err = rowSecurityGroups(auth, b.relation, principal, policyUpdate, true, true)
		}
	}
	return err
}
