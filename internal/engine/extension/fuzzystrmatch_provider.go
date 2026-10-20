package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Only the catalog-selected overloads are installed, not adjacent fuzzy functions.
type FuzzystrmatchProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *FuzzystrmatchProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *FuzzystrmatchProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}, Edges: []catalog.ExtensionVersionEdge{{From: "1.0", To: "1.1"}, {From: "1.1", To: "1.2"}}}
}

var StockFuzzystrmatch = &FuzzystrmatchProvider{control: catalog.ExtensionControl{Version: "1.2", Trusted: true, Superuser: true, Relocatable: true, Comment: new("determine similarities and distance between strings")}}

var FuzzystrmatchMembers = [...]struct {
	Name      string
	Signature catalog.ProcedureSignature
	Result    ids.OID
}{
	{"daitch_mokotoff", "25", 1009},
	{"levenshtein", "25,25", 23},
	{"levenshtein", "25,25,23,23,23", 23},
	{"levenshtein_less_equal", "25,25,23", 23},
	{"levenshtein_less_equal", "25,25,23,23,23,23", 23},
	{"metaphone", "25,23", 25},
	{"dmetaphone", "25", 25},
	{"dmetaphone_alt", "25", 25},
	{"soundex", "25", 25},
	{"difference", "25,25", 23},
}

func fuzzystrmatchResult(name string, signature catalog.ProcedureSignature) ids.OID {
	for _, member := range FuzzystrmatchMembers {
		if member.Name == name && member.Signature == signature {
			return member.Result
		}
	}
	return 0
}

func (p *FuzzystrmatchProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid fuzzystrmatch manifest") }
	count := len(FuzzystrmatchMembers)
	if ext.Version == "1.1" {
		count--
	}
	if v.Owner() == nil || p != StockFuzzystrmatch || ext.Name() != "fuzzystrmatch" || (ext.Version != "1.1" && ext.Version != "1.2") || len(ext.Members) != count {
		return invalid()
	}
	var seen [len(FuzzystrmatchMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Volatility != expr.Immutable || !proc.Strict || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		found := false
		for j, member := range FuzzystrmatchMembers {
			if ext.Version == "1.1" && j == 0 {
				continue
			}
			if proc.Name() == member.Name && proc.Signature == member.Signature && proc.Result == member.Result {
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
		// Membership fixes the exact provider, not separately transferable
		// function ownership. Every member retains real typed provenance.
		if proc.Owner.ID() == 0 || proc.Owner != v.Owner().Principals.Principal(proc.Owner.ID()) {
			return invalid()
		}
	}
	return nil
}

func (p *FuzzystrmatchProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing fuzzystrmatch installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || FuzzystrmatchProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: foreign fuzzystrmatch procedure")
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: nonmember fuzzystrmatch procedure")
}
