package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RowSecurity is the row-level security state of one command: whether the
// target's own rows are denied, and how the proposed rows of INSERT, UPDATE
// and MERGE are checked. The engine keeps it in its statement mutation, so
// command switches save and restore it as one unit.
type RowSecurity struct {
	Old, New      *exec.RLSProgram // Borrowed statement-owned ordinary command programs.
	TargetDenied  bool
	Proposal      access.AdjacentRLSDisposition
	MergeUpdate   access.AdjacentRLSDisposition
	MergeInsert   access.AdjacentRLSDisposition
	ProposalTable string
}

// RowSecurityTarget resets r for the command bound in b and returns the
// policy command that reads its target. The result is false, and nothing is
// left to select, for a statement without a target, a statement that is not
// INSERT, UPDATE, DELETE or MERGE (r keeps its proposal table then), and a
// target without row-level security.
func RowSecurityTarget(r RowSecurity, b analyze.Binding, stmt parser.Statement) (RowSecurity, catalog.PolicyCommand, bool) {
	r.TargetDenied = false
	r.Old, r.New = nil, nil
	r.Proposal, r.MergeInsert, r.MergeUpdate = access.AdjacentRLSUnrestricted, access.AdjacentRLSUnrestricted, access.AdjacentRLSUnrestricted
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

// SelectAdjacentRowSecurity retains only MERGE/ON CONFLICT action-specific
// expression capability boundaries. Ordinary sources and mutations use runtime
// programs and do not call this gate.
func SelectAdjacentRowSecurity(r RowSecurity, v access.View, enabled bool, b analyze.Binding, stmt parser.Statement, command catalog.PolicyCommand) (RowSecurity, error) {
	principal := access.RowSecurityPrincipal(v.State.CurrentUser, RowSecurityLayers(v.Catalog, b))
	if err := access.RowSecuritySettingGuard(enabled, v.Roles, b.Relation, principal); err != nil {
		return r, err
	}
	reads := b.Returning != nil || len(b.UsedColumns) != 0
	if b.ViewWrite != nil {
		reads = reads || len(ViewWriteOf(b.ViewWrite).Logical.UsedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		decision, err := access.AdjacentRowSecurityGroups(v.Roles, b.Relation, principal, command, false, reads)
		if err != nil {
			return r, err
		}
		if decision == access.AdjacentRLSNeedsExpressions {
			return r, access.AdjacentRowSecurityExpressionError()
		}
		r.TargetDenied = decision == access.AdjacentRLSDefaultDeny
	}
	proposedRead := b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0
	var err error
	switch stmt.(type) {
	case *parser.Insert:
		r.Proposal, err = access.AdjacentRowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyInsert, true, proposedRead)
	case *parser.Merge:
		r.MergeInsert, err = access.AdjacentRowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyInsert, true, false)
		if err == nil {
			r.MergeUpdate, err = access.AdjacentRowSecurityGroups(v.Roles, b.Relation, principal, catalog.PolicyUpdate, true, true)
		}
	}
	return r, err
}

// CheckProposal routes every ordinary NEW row to the same runtime evaluator;
// dispositions are retained only for explicitly gated adjacent actions.
func (r *RowSecurity) CheckProposal(row []sqltype.Value, evaluation *exec.Context) error {
	if r.New != nil {
		return exec.CheckRowSecurity(r.New, evaluation, row)
	}
	if r.Proposal != access.AdjacentRLSUnrestricted {
		return access.AdjacentRowSecurityProposal(r.Proposal, r.ProposalTable)
	}
	return nil
}
