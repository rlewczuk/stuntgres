package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

// RowSecurity is the row-level security state of one command: whether the
// target's own rows are denied, and how the proposed rows of INSERT, UPDATE
// and MERGE are checked. The engine keeps it in its statement mutation, so
// command switches save and restore it as one unit.
type RowSecurity struct {
	TargetDenied  bool
	Proposal      access.RLSDisposition
	MergeUpdate   access.RLSDisposition
	MergeInsert   access.RLSDisposition
	ProposalTable string
}

// RowSecurityTarget resets r for the command bound in b and returns the
// policy command that reads its target. The result is false, and nothing is
// left to select, for a statement without a target, a statement that is not
// INSERT, UPDATE, DELETE or MERGE (r keeps its proposal table then), and a
// target without row-level security.
func RowSecurityTarget(r RowSecurity, b analyze.Binding, stmt parser.Statement) (RowSecurity, catalog.PolicyCommand, bool) {
	r.TargetDenied = false
	r.Proposal, r.MergeInsert, r.MergeUpdate = access.RLSUnrestricted, access.RLSUnrestricted, access.RLSUnrestricted
	if b.Relation == nil {
		return r, "", false
	}
	var command catalog.PolicyCommand
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
		return r, "", false
	}
	r.ProposalTable = b.Relation.Name()
	return r, command, b.Relation.Security.RLSEnabled
}

// SelectRowSecurity selects the row-level security of a command whose target
// has it, as v's current user sees it through the view layers of b: the
// row_security setting guard (enabled is the setting), the target decision
// for command and the proposal groups. On an error it returns r with the
// fields written so far.
func SelectRowSecurity(r RowSecurity, v access.View, enabled bool, b analyze.Binding, stmt parser.Statement, command catalog.PolicyCommand) (RowSecurity, error) {
	principal := access.RowSecurityPrincipal(v.State.CurrentUser, RowSecurityLayers(v.Catalog, b))
	if err := access.RowSecuritySettingGuard(enabled, v.Roles, b.Relation, principal); err != nil {
		return r, err
	}
	reads := b.Returning != nil || len(b.UsedColumns) != 0
	if b.ViewWrite != nil {
		reads = reads || len(ViewWriteOf(b.ViewWrite).Logical.UsedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		decision, err := access.RowSecurityGroups(v.Roles, b.Relation, principal, command, false, reads)
		if err != nil {
			return r, err
		}
		if decision == access.RLSNeedsExpressions {
			return r, access.RowSecurityExpressionError()
		}
		r.TargetDenied = decision == access.RLSDefaultDeny
	}
	proposedRead := b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0
	var err error
	switch stmt.(type) {
	case *parser.Insert:
		r.Proposal, err = access.RowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyInsert, true, proposedRead)
	case *parser.Update:
		r.Proposal, err = access.RowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyUpdate, true, reads)
	case *parser.Merge:
		r.MergeInsert, err = access.RowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyInsert, true, false)
		if err == nil {
			r.MergeUpdate, err = access.RowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyUpdate, true, true)
		}
	}
	return r, err
}

// RowSecurityInputDenied reports whether row-level security on d denies every
// row principal reads (v's current user when principal is zero) for command:
// the setting guard (enabled is the row_security setting), then the policy
// groups. Policies that need expressions are an error.
func RowSecurityInputDenied(v access.View, enabled bool, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, selectRead bool) (bool, error) {
	if principal == (authorization.PrincipalRef{}) {
		principal = v.State.CurrentUser
	}
	if err := access.RowSecuritySettingGuard(enabled, v.Roles, d, principal); err != nil {
		return false, err
	}
	decision, err := access.RowSecurityGroups(v.Roles, d, principal, command, false, selectRead)
	if err != nil {
		return false, err
	}
	if decision == access.RLSNeedsExpressions {
		return false, access.RowSecurityExpressionError()
	}
	return decision == access.RLSDefaultDeny, nil
}
