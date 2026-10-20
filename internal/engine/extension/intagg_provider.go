package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type IntaggProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *IntaggProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *IntaggProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var StockIntagg = &IntaggProvider{control: catalog.ExtensionControl{Version: "1.1", Superuser: true, Relocatable: true, Comment: new("integer aggregator and enumerator (obsolete)")}}

var IntaggMembers = [...]struct {
	Name      string
	Signature catalog.ProcedureSignature
	Result    ids.OID
	Strict    bool
	Kind      catalog.RoutineKind
}{
	{"int_array_aggregate", "23", 1007, false, catalog.AggregateRoutine},
	{"int_array_enum", "1007", 23, true, catalog.FunctionRoutine},
}

func (p *IntaggProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intagg manifest") }
	if p != StockIntagg || v.Owner() == nil || ext.Name() != "intagg" || ext.Version != p.control.Version || len(ext.Members) != len(IntaggMembers) {
		return invalid()
	}
	var seen [len(IntaggMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Volatility != expr.Immutable || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		if err := validateOwnedAggregate(v, proc); err != nil {
			return err
		}
		found := false
		for j, member := range IntaggMembers {
			if proc.Name() == member.Name && proc.Signature == member.Signature && proc.Result == member.Result && proc.Strict == member.Strict && proc.RoutineKind == member.Kind {
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

func (p *IntaggProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intagg installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || IntaggProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: foreign intagg procedure")
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember intagg procedure")
}
