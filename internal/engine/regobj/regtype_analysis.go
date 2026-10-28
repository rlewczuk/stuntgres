package regobj

import (
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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
	domain            *ids.Domain
	epoch             ids.CatalogEpoch
	namespace         uint64
	configs           []configObservation
	configsRetired    bool
}

// Current observations never replace the original literal's operand provenance.
type configObservation struct {
	present  bool
	operands []catalog.ExpressionDependency
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
	cell.domain, cell.epoch = o.Domain, o.View.Catalog.Epoch()
	for _, constant := range constants {
		if configConstant(constant) {
			cell.namespace = o.View.Catalog.State().NamespaceGeneration()
			cell.configs = append(cell.configs, observeConfig(o, constant))
		}
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

// Invalidate retires ordinary aliases and the current analysis for good.
// Configuration input eligibility is separate and checked by ForReanalysis.
func (a *Analysis) Invalidate() { a.invalidated = true }

// RetireConfigurations makes an enclosing prepared-owner fence sticky even if
// a failed attempt later returns to the old session or private journal.
func (a *Analysis) RetireConfigurations() {
	if len(a.configs) != 0 {
		a.configsRetired = true
	}
}

// ForReanalysis projects only eligible configuration occurrences into this
// attempt. Other aliases keep their ordinary sticky source-reanalysis policy.
// A genuine resolution-environment change retires config captures even when
// the ensuing analysis fails and the environment is later restored.
func (a *Analysis) ForReanalysis(o Observation, paths LookupPaths) (*Analysis, error) {
	if a == nil || len(a.configs) == 0 || a.configsRetired {
		return nil, nil
	}
	path, err := paths.Type()
	if err != nil {
		return nil, err
	}
	if a.domain != o.Domain || a.epoch != o.View.Catalog.Epoch() ||
		a.role != o.View.State.CurrentUser || a.database != o.Database ||
		a.namespace != o.View.Catalog.State().NamespaceGeneration() ||
		!sequence.SameEnvironment(a.path, path) {
		a.configsRetired = true
		return nil, nil
	}
	constants := make([]analyze.RegtypeConstant, 0, len(a.configs))
	for _, constant := range a.Constants {
		if configConstant(constant) {
			allowed, err := qualifiedAllowed(o, constant)
			if err != nil {
				return nil, err
			}
			if !allowed {
				a.configsRetired = true
				return nil, nil
			}
			constants = append(constants, constant)
		}
	}
	return &Analysis{Constants: constants}, nil
}

func configConstant(c analyze.RegtypeConstant) bool {
	return c.Operator == sqltype.Regconfig && c.Value.Type() != nil && c.Value.Type().ID() == sqltype.Regconfig
}

func observeConfig(o Observation, c analyze.RegtypeConstant) configObservation {
	var current configObservation
	oid, err := c.Value.RegTSOID()
	if err != nil {
		return current
	}
	read := o.View.Catalog
	address := ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(oid)}
	if config, ok := read.TSConfiguration(address); ok {
		current.present = true
		current.operands = append(current.operands, catalog.ExpressionDependency{Ref: o.View.Ref(address), Revision: config.Revision()})
		if parser, ok := read.TSParser(config.Parser); ok {
			current.operands = append(current.operands, catalog.ExpressionDependency{Ref: o.View.Ref(config.Parser), Revision: parser.Revision()})
		}
		for _, mapping := range config.Mappings {
			for _, address := range mapping.Dictionaries {
				if slices.ContainsFunc(current.operands, func(d catalog.ExpressionDependency) bool { return d.Ref.Address == address }) {
					continue
				}
				if dictionary, ok := read.TSDictionary(address); ok {
					current.operands = append(current.operands, catalog.ExpressionDependency{Ref: o.View.Ref(address), Revision: dictionary.Revision()})
				}
			}
		}
	}
	return current
}

func qualifiedAllowed(o Observation, constant analyze.RegtypeConstant) (bool, error) {
	for _, namespace := range constant.Qualified {
		if o.View.Catalog.Get(namespace) == nil {
			return false, nil
		}
		if err := o.View.CanUse(o.Environment, o.View.Ref(namespace)); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

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
	configIndex := 0
	for _, constant := range a.Constants {
		if configConstant(constant) {
			if a.configsRetired || a.domain != o.Domain || a.epoch != view.Epoch() ||
				a.namespace != view.State().NamespaceGeneration() {
				return false, nil
			}
			current := a.configs[configIndex]
			configIndex++
			oid, err := constant.Value.RegTSOID()
			if err != nil {
				return false, err
			}
			_, present := view.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(oid)})
			if present != current.present {
				return false, nil
			}
			for _, operand := range current.operands {
				d := view.Get(operand.Ref.Address)
				if d == nil || d.Revision() != operand.Revision {
					return false, nil
				}
			}
			if allowed, err := qualifiedAllowed(o, constant); !allowed || err != nil {
				return false, err
			}
			continue
		}
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
