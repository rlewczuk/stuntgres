package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type IsnProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *IsnProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *IsnProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.3"}}
}

var StockIsn = &IsnProvider{control: catalog.ExtensionControl{Version: "1.3", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data types for international product numbering standards")}}

func IsnSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *IsnProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ean13 manifest") }
	if p != StockIsn || ext.Name() != "isn" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsEan13() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for i, address := range ext.Members[1:] {
		proc, ok := v.Procedure(address)
		if !ok || proc.Address() != address || IsnProviderOf(proc) != p || proc.Extension != ext.Address() || proc.Namespace() != ext.TargetNamespace || proc.Revision() == 0 {
			return invalid()
		}
		name, signature, result := "ean13_in", catalog.ProcedureSignature("2275"), ids.OID(base.SQLType.ID())
		if i == 1 {
			name, signature, result = "ean13_out", IsnSignature(base.SQLType.ID()), 2275
		}
		if proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		if proc.RoutineKind != catalog.FunctionRoutine || proc.Aggregate != nil || proc.Foreign != nil || proc.Builtin != nil {
			return invalid()
		}
		if proc.Name() != name || proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func (p *IsnProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if proc == nil || v.Get(proc.Address()) != proc {
		return fmt.Errorf("stuntgres: foreign ean13 codec procedure")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing ean13 codec installation")
	}
	if len(ext.Members) != 3 || proc.Address() != ext.Members[1] && proc.Address() != ext.Members[2] {
		return fmt.Errorf("stuntgres: foreign ean13 codec member")
	}
	return p.ValidateExtension(v, ext)
}
