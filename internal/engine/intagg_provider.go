package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type intaggProvider struct{ control catalog.ExtensionControl }

// Control implements extensionImplementation.
func (p *intaggProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *intaggProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var stockIntagg = &intaggProvider{control: catalog.ExtensionControl{Version: "1.1", Superuser: true, Relocatable: true, Comment: new("integer aggregator and enumerator (obsolete)")}}

var intaggMembers = [...]struct {
	name      string
	signature catalog.ProcedureSignature
	result    ids.OID
	strict    bool
	kind      catalog.RoutineKind
}{
	{"int_array_aggregate", "23", 1007, false, catalog.AggregateRoutine},
	{"int_array_enum", "1007", 23, true, catalog.FunctionRoutine},
}

func (p *intaggProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg manifest") }
	if p != stockIntagg || v.Owner() == nil || ext.Name() != "intagg" || ext.Version != p.control.Version || len(ext.Members) != len(intaggMembers) {
		return invalid()
	}
	var seen [len(intaggMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Volatility != expr.Immutable || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		if err := validateOwnedAggregate(v, proc); err != nil {
			return err
		}
		found := false
		for j, member := range intaggMembers {
			if proc.Name() == member.name && proc.Signature == member.signature && proc.Result == member.result && proc.Strict == member.strict && proc.RoutineKind == member.kind {
				if seen[j] {
					return invalid()
				}
				seen[j], found = true, true
				break
			}
		}
		if !found {
			return invalid()
		}
		if proc.Owner.ID() == 0 || proc.Owner != v.Owner().Principals.Principal(proc.Owner.ID()) {
			return invalid()
		}
	}
	return nil
}

func (p *intaggProvider) validateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intagg installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || intaggProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: foreign intagg procedure")
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember intagg procedure")
}
