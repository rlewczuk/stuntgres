package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type PgrowlocksProvider struct{ control catalog.ExtensionControl }

var StockPgrowlocks = &PgrowlocksProvider{control: catalog.ExtensionControl{
	Version: "1.2", Superuser: true, Relocatable: true,
	Comment: new("show row-level locking information"),
}}

func (p *PgrowlocksProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *PgrowlocksProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}
func PgrowlocksProviderOf(p *catalog.Procedure) *PgrowlocksProvider {
	provider, _ := p.Implementation.(*PgrowlocksProvider)
	return provider
}

func (p *PgrowlocksProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid pgrowlocks manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockPgrowlocks || v.Owner() == nil || ext.Name() != "pgrowlocks" || ext.Version != "1.2" || len(ext.Members) != 1 {
		return invalid()
	}
	proc, ok := v.Procedure(ext.Members[0])
	if !ok {
		return invalid()
	}
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	return nil
}

func (p *PgrowlocksProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgrowlocks installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || ext.Members[0] != proc.Address() {
		return fmt.Errorf("stuntgres: foreign pgrowlocks procedure")
	}
	return nil
}

func ValidatePgrowlocksIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain ||
		ref.Epoch != v.Epoch() || PgrowlocksProviderOf(proc) != StockPgrowlocks {
		return fmt.Errorf("stuntgres: invalid stored pgrowlocks identity")
	}
	return StockPgrowlocks.ValidateProcedure(v, proc)
}
