package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// Empty protected inputs still own their immutable visibility catalog. Do not
// capture physical prefixes or permit later live-version selection to revive rows.
func captureDeniedQuerySource(view *visibility.View, ref ids.ObjectRef) (*exec.Source, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, fmt.Errorf("stuntgres: visibility view is released")
	}
	data := pin.Data()
	if data.Catalog.Owner() == nil || ref.Domain() != data.Catalog.Owner().Domain || ref.Epoch != data.Catalog.Epoch() || data.Catalog.Get(ref.Address) == nil {
		pin.Release()
		return nil, fmt.Errorf("stuntgres: invalid visibility target")
	}
	return &exec.Source{View: pin, Ref: ref, RlsDenied: true}, nil
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
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
	auth.Recapture(auth.Catalog)
	if principal == (authorization.PrincipalRef{}) {
		principal = auth.State.CurrentUser
	}
	if err := access.RowSecuritySettingGuard(scope.rowSecurity, auth.Roles, d, principal); err != nil {
		return false, err
	}
	decision, err := access.RowSecurityGroups(auth.Roles, d, principal, command, false, selectRead)
	if err != nil {
		return false, err
	}
	if decision == access.RLSNeedsExpressions {
		return false, access.RowSecurityExpressionError()
	}
	return decision == access.RLSDefaultDeny, nil
}

// rowSecurityLayers collects the owner and security_invoker flag of each
// view layer of an auto-updatable view write, outermost first.
func rowSecurityLayers(view catalog.Read, b analyze.Binding) []access.RowSecurityLayer {
	if b.ViewWrite == nil {
		return nil
	}
	layers := make([]access.RowSecurityLayer, 0, len(viewWriteOf(b.ViewWrite).layers))
	for _, layer := range viewWriteOf(b.ViewWrite).layers {
		current := view.MustRelation(layer.ref.Address)
		layers = append(layers, access.RowSecurityLayer{Owner: current.Owner, Invoker: viewOf(current).Invoker()})
	}
	return layers
}

func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b analyze.Binding, stmt parser.Statement) error {
	scope.rlsTargetDenied = false
	scope.rlsProposal, scope.rlsMergeInsert, scope.rlsMergeUpdate = access.RLSUnrestricted, access.RLSUnrestricted, access.RLSUnrestricted
	if b.Relation == nil {
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
	scope.rlsProposalTable = b.Relation.Name()
	if !b.Relation.Security.RLSEnabled {
		return nil
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return err
	}
	if scope.ruleActionAuth != nil {
		auth.State.CurrentUser = scope.ruleActionAuth.State.CurrentUser
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
	auth.Recapture(auth.Catalog)
	principal := access.RowSecurityPrincipal(auth.State.CurrentUser, rowSecurityLayers(auth.Catalog, b))
	if err := access.RowSecuritySettingGuard(scope.rowSecurity, auth.Roles, b.Relation, principal); err != nil {
		return err
	}
	reads := b.Returning != nil || len(b.UsedColumns) != 0
	if b.ViewWrite != nil {
		reads = reads || len(viewWriteOf(b.ViewWrite).logical.UsedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		decision, err := access.RowSecurityGroups(auth.Roles, b.Relation, principal, command, false, reads)
		if err != nil {
			return err
		}
		if decision == access.RLSNeedsExpressions {
			return access.RowSecurityExpressionError()
		}
		scope.rlsTargetDenied = decision == access.RLSDefaultDeny
	}
	proposedRead := b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0
	switch stmt.(type) {
	case *parser.Insert:
		scope.rlsProposal, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyInsert, true, proposedRead)
	case *parser.Update:
		scope.rlsProposal, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyUpdate, true, reads)
	case *parser.Merge:
		scope.rlsMergeInsert, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyInsert, true, false)
		if err == nil {
			scope.rlsMergeUpdate, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyUpdate, true, true)
		}
	}
	return err
}
