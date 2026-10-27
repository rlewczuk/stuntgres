package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// TablefuncProvider owns the selected released tablefunc routines.
type TablefuncProvider struct{ control catalog.ExtensionControl }

var StockTablefunc = &TablefuncProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Trusted: true, Relocatable: true,
	Comment: new("functions that manipulate whole tables, including crosstab"),
}}

type tablefuncMember struct {
	name, source string
	signature    catalog.ProcedureSignature
	result       ids.OID
	volatility   expr.Volatility
}

// Slot zero is incumbent normal_rand. Appended slots follow the release script.
var tablefuncMembers = [...]tablefuncMember{
	{"normal_rand", "normal_rand", "23,701,701", 701, expr.Volatile},
	{"connectby", "connectby_text", "25,25,25,25,23,25", 2249, expr.Stable},
	{"connectby", "connectby_text", "25,25,25,25,23", 2249, expr.Stable},
	{"connectby", "connectby_text_serial", "25,25,25,25,25,23,25", 2249, expr.Stable},
	{"connectby", "connectby_text_serial", "25,25,25,25,25,23", 2249, expr.Stable},
}

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
	members := make([]ids.ObjectAddress, 0, len(tablefuncMembers))
	for _, member := range tablefuncMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.name, Signature: member.signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: member.result, Volatility: member.volatility, Strict: true, Installed: tablefuncNormalDeclaration(), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.source}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		members = append(members, address)
	}
	ext.Members = members
	return nil
}
func (p *TablefuncProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	if !currentManifest(v, ext) || p != StockTablefunc || v.Owner() == nil || ext.Name() != "tablefunc" || ext.Version != "1.0" || len(ext.Members) != len(tablefuncMembers) {
		return fmt.Errorf("stuntgres: invalid tablefunc manifest")
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return fmt.Errorf("stuntgres: invalid tablefunc member")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}
func (p *TablefuncProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing tablefunc installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || TablefuncProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: foreign tablefunc procedure")
	}
	_, err := ProcedureSymbol(v, proc)
	return err
}
func ValidateTablefuncIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || TablefuncProviderOf(proc) != StockTablefunc {
		return fmt.Errorf("stuntgres: invalid stored tablefunc identity")
	}
	return StockTablefunc.ValidateProcedure(v, proc)
}
