package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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
	if data.catalog.Owner() == nil || ref.Domain() != data.catalog.Owner().Domain || ref.Epoch != data.catalog.Epoch() || data.catalog.Get(ref.Address) == nil {
		pin.Release()
		return nil, fmt.Errorf("stuntgres: invalid visibility target")
	}
	return &querySource{view: pin, ref: ref, rlsDenied: true}, nil
}

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

func rowSecurityFlags(old *catalog.Relation, actions []parser.AlterTableAction) *catalog.Relation {
	enabled, forced := old.Security.RLSEnabled, old.Security.RLSForced
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
	if enabled == old.Security.RLSEnabled && forced == old.Security.RLSForced {
		return old
	}
	next := *old
	next.Security.RLSEnabled, next.Security.RLSForced = enabled, forced
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

func rowSecurityKind(d *catalog.Relation, action parser.AlterTableActionKind) error {
	if d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.PartitionedRelation {
		return nil
	}
	kind := ""
	switch d.RelationKind {
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.SequenceRelation:
		kind = "sequences"
	case catalog.IndexRelation:
		kind = "indexes"
	case catalog.CompositeRelation:
		kind = "composite types"
	default:
		return tableKind(d, rowSecurityActionLabel(action))
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, rowSecurityActionLabel(action), d.Name()), Detail: "This operation is not supported for " + kind + "."}
}

// The caller holds Engine.mu; this never captures row data or mutates identity.
func (e *Engine) rowSecurityInputLocked(scope *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, selectRead bool) (bool, error) {
	if !d.Security.RLSEnabled {
		return false, nil
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return false, err
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.Recapture(auth.Catalog)
	if principal == (authorization.PrincipalRef{}) {
		principal = auth.State.CurrentUser
	}
	decision, err := access.RowSecurityGroups(auth.Roles, d, principal, command, false, selectRead)
	if err != nil {
		return false, err
	}
	if decision == rlsNeedsExpressions {
		return false, access.RowSecurityExpressionError()
	}
	return decision == rlsDefaultDeny, nil
}

// rowSecurityLayers collects the owner and security_invoker flag of each
// view layer of an auto-updatable view write, outermost first.
func rowSecurityLayers(view catalog.Read, b binding) []access.RowSecurityLayer {
	if b.viewWrite == nil {
		return nil
	}
	layers := make([]access.RowSecurityLayer, 0, len(b.viewWrite.layers))
	for _, layer := range b.viewWrite.layers {
		current := view.MustRelation(layer.ref.Address)
		layers = append(layers, access.RowSecurityLayer{Owner: current.Owner, Invoker: viewOf(current).invoker})
	}
	return layers
}

func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b binding, stmt parser.Statement) error {
	scope.rlsTargetDenied = false
	scope.rlsProposal, scope.rlsMergeInsert, scope.rlsMergeUpdate = rlsUnrestricted, rlsUnrestricted, rlsUnrestricted
	if b.relation == nil {
		return nil
	}
	command := catalog.PolicyCommand("")
	switch stmt.(type) {
	case *parser.Insert:
		command = catalog.PolicyInsert
	case *parser.Update:
		command = catalog.PolicyUpdate
	case *parser.Delete:
		command = catalog.PolicyDelete
	case *parser.Merge:
		command = catalog.PolicySelect
	default:
		return nil
	}
	scope.rlsProposalTable = b.relation.Name()
	if !b.relation.Security.RLSEnabled {
		return nil
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return err
	}
	if scope.ruleActionAuth != nil {
		auth.State.CurrentUser = scope.ruleActionAuth.State.CurrentUser
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.catalog)
	auth.Recapture(auth.Catalog)
	principal := access.RowSecurityPrincipal(auth.State.CurrentUser, rowSecurityLayers(auth.Catalog, b))
	reads := b.returning != nil || len(b.usedColumns) != 0
	if b.viewWrite != nil {
		reads = reads || len(b.viewWrite.logical.usedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		decision, err := access.RowSecurityGroups(auth.Roles, b.relation, principal, command, false, reads)
		if err != nil {
			return err
		}
		if decision == rlsNeedsExpressions {
			return access.RowSecurityExpressionError()
		}
		scope.rlsTargetDenied = decision == rlsDefaultDeny
	}
	proposedRead := b.conflict != nil && len(b.conflict.reads) != 0
	switch stmt.(type) {
	case *parser.Insert:
		scope.rlsProposal, err = access.RowSecurityGroups(auth.Roles, b.relation, principal, catalog.PolicyInsert, true, proposedRead)
	case *parser.Update:
		scope.rlsProposal, err = access.RowSecurityGroups(auth.Roles, b.relation, principal, catalog.PolicyUpdate, true, reads)
	case *parser.Merge:
		scope.rlsMergeInsert, err = access.RowSecurityGroups(auth.Roles, b.relation, principal, catalog.PolicyInsert, true, false)
		if err == nil {
			scope.rlsMergeUpdate, err = access.RowSecurityGroups(auth.Roles, b.relation, principal, catalog.PolicyUpdate, true, true)
		}
	}
	return err
}
