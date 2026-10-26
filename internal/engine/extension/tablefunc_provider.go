package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// TablefuncProvider owns the released normal_rand installation symbol.
type TablefuncProvider struct{ control catalog.ExtensionControl }

var StockTablefunc = &TablefuncProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Trusted: true, Relocatable: true,
	Comment: new("functions that manipulate whole tables, including crosstab"),
}}

func (p *TablefuncProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *TablefuncProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func TablefuncProviderOf(p *catalog.Procedure) *TablefuncProvider {
	provider, _ := p.Implementation.(*TablefuncProvider)
	return provider
}
func tablefuncNormalDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000}
}
func (p *TablefuncProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "normal_rand", Signature: "23,701,701", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 701, Volatility: expr.Volatile, Strict: true, Installed: tablefuncNormalDeclaration(), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "normal_rand"}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	if err := stage.Create(proc); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
func (p *TablefuncProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	if !currentManifest(v, ext) || p != StockTablefunc || v.Owner() == nil || ext.Name() != "tablefunc" || ext.Version != "1.0" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid tablefunc manifest")
	}
	proc, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: invalid tablefunc member") }
	_, err := ProcedureSymbol(v, proc)
	return err
}
func (p *TablefuncProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok { return fmt.Errorf("stuntgres: missing tablefunc installation") }
	if err := p.ValidateExtension(v, ext); err != nil { return err }
	if v.Get(proc.Address()) != proc || TablefuncProviderOf(proc) != p || ext.Members[0] != proc.Address() {
		return fmt.Errorf("stuntgres: foreign tablefunc procedure")
	}
	return nil
}
func ValidateTablefuncIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || TablefuncProviderOf(proc) != StockTablefunc {
		return fmt.Errorf("stuntgres: invalid stored tablefunc identity")
	}
	return StockTablefunc.ValidateProcedure(v, proc)
}
