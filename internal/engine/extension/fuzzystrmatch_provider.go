package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
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
	Source    string
}{
	{"daitch_mokotoff", "25", 1009, "daitch_mokotoff"},
	{"levenshtein", "25,25", 23, "levenshtein"},
	{"levenshtein", "25,25,23,23,23", 23, "levenshtein_with_costs"},
	{"levenshtein_less_equal", "25,25,23", 23, "levenshtein_less_equal"},
	{"levenshtein_less_equal", "25,25,23,23,23,23", 23, "levenshtein_less_equal_with_costs"},
	{"metaphone", "25,23", 25, "metaphone"},
	{"dmetaphone", "25", 25, "dmetaphone"},
	{"dmetaphone_alt", "25", 25, "dmetaphone_alt"},
	{"soundex", "25", 25, "soundex"},
	{"difference", "25,25", 23, "difference"},
}

func (p *FuzzystrmatchProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid fuzzystrmatch manifest") }
	count := len(FuzzystrmatchMembers)
	if ext.Version == "1.1" {
		count--
	}
	if !currentManifest(v, ext) {
		return invalid()
	}
	if v.Owner() == nil || p != StockFuzzystrmatch || ext.Name() != "fuzzystrmatch" || (ext.Version != "1.1" && ext.Version != "1.2") || len(ext.Members) != count {
		return invalid()
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
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
