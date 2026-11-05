package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type XML2Provider struct{ control catalog.ExtensionControl }

var StockXML2 = &XML2Provider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Comment: new("XPath querying and XSLT")}}

func (p *XML2Provider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *XML2Provider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}
func (p *XML2Provider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *XML2Provider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
func XML2ProviderOf(proc *catalog.Procedure) *XML2Provider {
	p, _ := proc.Implementation.(*XML2Provider)
	return p
}
func (p *XML2Provider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockXML2 || !currentManifest(v, e) || e.Name() != "xml2" || e.Version != "1.2" || len(e.Members) != len(functions.XML2Members) {
		return fmt.Errorf("invalid xml2 manifest")
	}
	for _, a := range e.Members {
		proc, ok := v.Procedure(a)
		if !ok {
			return fmt.Errorf("missing xml2 declaration")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}

func ValidateXML2XPathTableIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || XML2ProviderOf(proc) != StockXML2 {
		return fmt.Errorf("invalid installed xpath_table identity")
	}
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return err
	}
	if symbol != "xpath_table" {
		return fmt.Errorf("invalid installed xpath_table selector")
	}
	return nil
}
