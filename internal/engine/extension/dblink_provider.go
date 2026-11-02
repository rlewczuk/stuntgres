package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// DblinkProvider installs local SQL constructors and primary-key metadata.
// It does not install connection routines.
type DblinkProvider struct{ control catalog.ExtensionControl }

var StockDblink = &DblinkProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("connect to other PostgreSQL databases from within a database")}}

func (p *DblinkProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*DblinkProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}
func DblinkProviderOf(p *catalog.Procedure) *DblinkProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*DblinkProvider)
	return provider
}
func (p *DblinkProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockDblink || !currentManifest(v, ext) || ext.Name() != "dblink" || ext.Version != "1.2" || len(ext.Members) != dblinkMemberCount {
		return fmt.Errorf("stuntgres: invalid dblink manifest")
	}
	for _, slot := range [...]int{0, 1, 2, dblinkPkeyProcedureSlot} {
		member, ok := v.Procedure(ext.Members[slot])
		if !ok || member.Origin == nil || member.Origin.Declaration != slot {
			return fmt.Errorf("stuntgres: missing dblink member")
		}
		if _, err := ProcedureSymbol(v, member); err != nil {
			return err
		}
	}
	for _, slot := range [...]int{dblinkPkeyTypeSlot, dblinkPkeyArraySlot} {
		typ, ok := v.Type(ext.Members[slot])
		if !ok {
			return fmt.Errorf("stuntgres: missing dblink result type")
		}
		if err := ValidateTypeOrigin(v, typ); err != nil {
			return err
		}
	}
	return nil
}
func (p *DblinkProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func (p *DblinkProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}

var dblinkMembers = [...]struct {
	name      string
	signature catalog.ProcedureSignature
}{
	{functions.DblinkDeleteName, functions.DblinkDeleteSignature},
	{functions.DblinkInsertName, functions.DblinkInsertSignature},
	{functions.DblinkUpdateName, functions.DblinkUpdateSignature},
}

func (p *DblinkProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	destination := ext
	candidate := *ext
	ext = &candidate
	ext.Members = make([]ids.ObjectAddress, dblinkMemberCount)
	createProcedure := func(slot int, name string, signature catalog.ProcedureSignature, result ids.OID, declaration catalog.InstalledRoutineDeclaration) error {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{
			ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision,
			ObjectName: name, Signature: signature, Owner: owner,
			RoutineKind: catalog.FunctionRoutine, Result: result, Volatility: expr.Volatile, Strict: true,
			Installed: declaration,
			Display:   catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/dblink", HasBinary: true}, Implementation: p,
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members[slot] = address
		return nil
	}
	for slot, member := range dblinkMembers {
		if err := createProcedure(slot, member.name, member.signature, 25, catalog.InstalledRoutineDeclaration{Parallel: 'r'}); err != nil {
			return err
		}
	}
	int4, _ := stage.Read().LookupType(sqltype.Int4)
	text, _ := stage.Read().LookupType(sqltype.Text)
	columns := []catalog.Column{
		{Name: "position", Usage: int4.Usage(), Nullable: true, NullabilityKnown: true},
		{Name: "colname", Usage: text.Usage(), Nullable: true, NullabilityKnown: true},
	}
	typ, array, err := stage.Composite(ext.TargetNamespace, dblinkPkeyTypeRole, owner, columns, ext.Address())
	if err != nil {
		return err
	}
	ext.Members[dblinkPkeyTypeSlot], ext.Members[dblinkPkeyArraySlot] = typ.Address(), array.Address()
	if err := createProcedure(dblinkPkeyProcedureSlot, functions.DblinkGetPkeyName, "25", typ.Address().ObjectID, dblinkPkeyDeclaration()); err != nil {
		return err
	}
	destination.Members = ext.Members
	return nil
}
func ValidateDblinkIdentity(v catalog.Read, p *catalog.Procedure, ref ids.ObjectRef) error {
	current, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || p == nil || current != p || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || DblinkProviderOf(p) != StockDblink {
		return fmt.Errorf("stuntgres: invalid stored dblink identity")
	}
	return StockDblink.ValidateProcedure(v, p)
}
func ValidateDblinkBuildSQLDeleteCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkBuildSQLDelete) error {
	if p == nil || DblinkProviderOf(p) != StockDblink || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid dblink constructor identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if symbol != functions.DblinkDeleteName || p.Signature != functions.DblinkDeleteSignature {
		return fmt.Errorf("stuntgres: mismatched dblink constructor")
	}
	if err := call.ValidateArguments(call.Args[:]); err != nil {
		return err
	}
	typ, ok := v.LookupType(sqltype.Text)
	if !ok || call.ResultUsage() != typ.Usage() {
		return fmt.Errorf("stuntgres: invalid dblink constructor result")
	}
	return nil
}

func ValidateDblinkBuildSQLInsertCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkBuildSQLInsert) error {
	if p == nil || DblinkProviderOf(p) != StockDblink || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid dblink constructor identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if symbol != functions.DblinkInsertName || p.Signature != functions.DblinkInsertSignature {
		return fmt.Errorf("stuntgres: mismatched dblink constructor")
	}
	if err := call.ValidateArguments(call.Args[:]); err != nil {
		return err
	}
	typ, ok := v.LookupType(sqltype.Text)
	if !ok || call.ResultUsage() != typ.Usage() {
		return fmt.Errorf("stuntgres: invalid dblink constructor result")
	}
	return nil
}

func ValidateDblinkBuildSQLUpdateCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkBuildSQLUpdate) error {
	if p == nil || DblinkProviderOf(p) != StockDblink || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid dblink constructor identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if symbol != functions.DblinkUpdateName || p.Signature != functions.DblinkUpdateSignature {
		return fmt.Errorf("stuntgres: mismatched dblink constructor")
	}
	if err := call.ValidateArguments(call.Args[:]); err != nil {
		return err
	}
	typ, ok := v.LookupType(sqltype.Text)
	if !ok || call.ResultUsage() != typ.Usage() {
		return fmt.Errorf("stuntgres: invalid dblink constructor result")
	}
	return nil
}
