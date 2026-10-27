package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type HstoreProvider struct{ control catalog.ExtensionControl }

const (
	HstoreTypeSlot = iota
	HstoreInputSlot
	HstoreOutputSlot
	HstoreArrayProcedureSlot
	HstoreMatrixProcedureSlot
	HstoreJSONProcedureSlot
	HstoreJSONLooseProcedureSlot
	HstoreArrayOperatorSlot
	HstoreMatrixOperatorSlot
	HstoreJSONBProcedureSlot
	HstoreJSONBLooseProcedureSlot
	HstoreJSONBCastSlot
	HstoreComparisonProcedureStart
	HstoreComparisonOperatorStart = HstoreComparisonProcedureStart + 9
	HstoreBtreeFamilySlot = HstoreComparisonOperatorStart + 6
	HstoreBtreeClassSlot = HstoreBtreeFamilySlot + 1
	HstoreHashFamilySlot = HstoreBtreeClassSlot + 1
	HstoreHashClassSlot = HstoreHashFamilySlot + 1
	HstoreMemberCount = HstoreHashClassSlot + 1
)

var StockHstore = &HstoreProvider{control: catalog.ExtensionControl{Version: "1.8", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for storing sets of (key, value) pairs")}}

func (p *HstoreProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *HstoreProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.8"}}
}

func HstoreSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *HstoreProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid hstore manifest") }
	if !currentManifest(v, ext) || p != StockHstore || ext.Name() != "hstore" || ext.Version != p.control.Version || len(ext.Members) != HstoreMemberCount {
		return invalid()
	}
	seen := make(map[ids.ObjectAddress]bool, HstoreMemberCount)
	for i, address := range ext.Members {
		class := hstoreMemberClass(i)
		if address.ClassID != class || address.ObjectID == 0 || address.SubID != 0 || seen[address] {
			return invalid()
		}
		seen[address] = true
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsHstore() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for _, slot := range [...]int{HstoreInputSlot, HstoreOutputSlot, HstoreArrayProcedureSlot, HstoreMatrixProcedureSlot, HstoreJSONProcedureSlot, HstoreJSONLooseProcedureSlot, HstoreJSONBProcedureSlot, HstoreJSONBLooseProcedureSlot} {
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for i := range 2 {
		if err := validateHstoreOperator(v, ext, base, i); err != nil {
			return err
		}
	}
	cast, ok := v.Cast(base.SQLType.ID(), sqltype.JSONB)
	if !ok || cast.Address() != ext.Members[HstoreJSONBCastSlot] || cast.Extension != ext.Address() || cast.Function != ext.Members[HstoreJSONBProcedureSlot] || cast.Context != "e" || cast.Method != "f" || cast.InputCast != (ids.ObjectAddress{}) || cast.ResultCast != (ids.ObjectAddress{}) || cast.GeneratedRange != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := (catalog.CastProvider{}).Validate(v, cast); err != nil {
		return err
	}
	return validateHstoreComparisons(v, ext, base)
}

func (p *HstoreProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing hstore codec installation")
	}
	return p.ValidateExtension(v, ext)
}

func validateHstoreOperator(v catalog.Read, ext *catalog.Extension, base *catalog.Type, i int) error {
	op, ok := v.Operator(ext.Members[HstoreArrayOperatorSlot+i])
	if !ok || op.Address() != ext.Members[HstoreArrayOperatorSlot+i] || op.Namespace() != ext.TargetNamespace || op.Name() != []string{"%%", "%#"}[i] || op.Extension != ext.Address() || op.Left != 0 || op.Right != ids.OID(base.SQLType.ID()) || op.Result != 1009 || op.Function != ext.Members[HstoreArrayProcedureSlot+i].ObjectID || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge {
		return fmt.Errorf("stuntgres: invalid hstore operator")
	}
	return ValidateProviderHeader(v, op, ids.OperatorClassID)
}
