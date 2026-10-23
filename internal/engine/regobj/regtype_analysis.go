package regobj

import (
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqlstate"
)

// Analysis is a prepared statement's analysis of its reg* literals: the
// captured constants, and the role, database and lookup paths they were
// resolved under. Execution reuses the constants while Valid holds.
type Analysis struct {
	Constants         []analyze.RegtypeConstant
	path              sequence.Environment
	role              authorization.PrincipalRef
	database          string
	types, collations bool
	collationPath     []ids.ObjectAddress
	invalidated       bool // A failed reanalysis must not revive these captures.
}

// Observation is what an Analysis is captured and validated against: the
// catalog domain, the session's database, and the statement's access.View
// with its environment.
type Observation struct {
	Domain      *ids.Domain
	Database    string
	View        access.View
	Environment access.Environment
}

// LookupPaths reads the statement's type and collation lookup paths. Each is
// called only when the constants need it; the caller holds any lock they
// need. It is kept apart from Observation so the callbacks do not escape
// with the environment.
type LookupPaths struct {
	Type      func() (sequence.Environment, error)
	Collation func() ([]ids.ObjectAddress, error)
}

// NewAnalysis captures constants under o. With no constants it returns nil.
// It reads the type path for type, operator and routine constants and the
// collation path for collation references.
func NewAnalysis(o Observation, paths LookupPaths, constants []analyze.RegtypeConstant) (*Analysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	cell := &Analysis{Constants: slices.Clone(constants), role: o.View.State.CurrentUser, database: o.Database}
	for _, constant := range constants {
		if constant.Operator != 0 || constant.Value.Type() == nil || constant.Value.Type().ContainsRegtype() || constant.Value.Type().ContainsOperatorAlias() || constant.Value.Type().ContainsRegproc() {
			cell.types = true
		}
		for _, reference := range constant.References {
			cell.collations = cell.collations || reference.Address.ClassID == ids.CollationClassID
		}
	}
	var err error
	if cell.types {
		cell.path, err = paths.Type()
	}
	if err == nil && cell.collations {
		cell.collationPath, err = paths.Collation()
	}
	return cell, err
}

// Invalidate retires a's captures for good: a later failed reanalysis must
// not make them valid again.
func (a *Analysis) Invalidate() { a.invalidated = true }

// Valid reports whether a still holds under o: the same role, database and
// lookup paths, unchanged operand stamps and reference names, and USAGE on
// the namespaces of collation references and qualified names. A nil a is
// valid; an invalidated a is not.
func (a *Analysis) Valid(o Observation, paths LookupPaths) (bool, error) {
	if a != nil && a.invalidated {
		return false, nil
	}
	if a == nil {
		return true, nil
	}
	if a.types {
		path, err := paths.Type()
		if err != nil {
			return false, err
		}
		if a.role != o.View.State.CurrentUser || a.database != o.Database || !sequence.SameEnvironment(a.path, path) {
			return false, nil
		}
	}
	if a.collations {
		path, err := paths.Collation()
		if err != nil {
			return false, err
		}
		if a.role != o.View.State.CurrentUser || a.database != o.Database || !slices.Equal(a.collationPath, path) {
			return false, nil
		}
	}
	// Preserve qualified namespace authority and operator operand stamps.
	// Collation/namespace aliases additionally reanalyze changed captured names.
	view := o.View.Catalog
	for _, constant := range a.Constants {
		for _, operand := range constant.Operands {
			d := view.Get(operand.Ref.Address)
			if d == nil || operand.Ref.Domain() != o.Domain || operand.Ref.Epoch != view.Epoch() || d.Revision() != operand.Revision {
				return false, nil
			}
		}
		for _, reference := range constant.References {
			d := view.Get(reference.Address)
			if d == nil || d.Name() != reference.Name || d.Namespace() != reference.Namespace {
				return false, nil
			}
			if reference.Address.ClassID == ids.CollationClassID {
				ns := view.Get(reference.Namespace)
				if ns == nil || ns.Name() != reference.NamespaceName {
					return false, nil
				}
				if err := o.View.CanUse(o.Environment, o.Domain.Ref(view.Epoch(), reference.Namespace)); err != nil {
					if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
						return false, nil
					}
					return false, err
				}
			}
		}
		for _, namespace := range constant.Qualified {
			if view.Get(namespace) == nil {
				return false, nil
			}
			if err := o.View.CanUse(o.Environment, o.Domain.Ref(view.Epoch(), namespace)); err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
					return false, nil
				}
				return false, err
			}
		}
	}
	return true, nil
}
