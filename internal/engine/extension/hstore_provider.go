package extension

import (
	"fmt"
	"strconv"
	"strings"

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
	HstoreBtreeFamilySlot         = HstoreComparisonOperatorStart + 6
	HstoreBtreeClassSlot          = HstoreBtreeFamilySlot + 1
	HstoreHashFamilySlot          = HstoreBtreeClassSlot + 1
	HstoreHashClassSlot           = HstoreHashFamilySlot + 1
)

const (
	HstoreTextConstructorSlot = HstoreHashClassSlot + 1 + iota
	HstoreArraysConstructorSlot
	HstoreArrayConstructorSlot
	HstoreFetchSlot
	HstoreSliceSlot
	HstoreExistsSlot
	HstoreDefinedSlot
	HstoreFetchOperatorSlot
	HstoreSliceOperatorSlot
	HstoreMemberCount
)

var StockHstore = &HstoreProvider{control: catalog.ExtensionControl{Version: "1.8", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for storing sets of (key, value) pairs")}}

func (p *HstoreProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *HstoreProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.8"}}
}

func HstoreSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}

type hstoreRoutine struct {
	slot                    int
	name, symbol, signature string
	result                  sqltype.ID
	strict                  bool
}

var hstoreRoutines = [...]hstoreRoutine{
	{HstoreInputSlot, "hstore_in", "hstore_in", "2275", 0, true},
	{HstoreOutputSlot, "hstore_out", "hstore_out", "hstore", 2275, true},
	{HstoreArrayProcedureSlot, "hstore_to_array", "hstore_to_array", "hstore", 1009, true},
	{HstoreMatrixProcedureSlot, "hstore_to_matrix", "hstore_to_matrix", "hstore", 1009, true},
	{HstoreJSONProcedureSlot, "hstore_to_json", "hstore_to_json", "hstore", 114, true},
	{HstoreJSONLooseProcedureSlot, "hstore_to_json_loose", "hstore_to_json_loose", "hstore", 114, true},
	{HstoreJSONBProcedureSlot, "hstore_to_jsonb", "hstore_to_jsonb", "hstore", 3802, true},
	{HstoreJSONBLooseProcedureSlot, "hstore_to_jsonb_loose", "hstore_to_jsonb_loose", "hstore", 3802, true},
	{HstoreTextConstructorSlot, "hstore", "hstore_from_text", "25,25", 0, false},
	{HstoreArraysConstructorSlot, "hstore", "hstore_from_arrays", "1009,1009", 0, false},
	{HstoreArrayConstructorSlot, "hstore", "hstore_from_array", "1009", 0, true},
	{HstoreFetchSlot, "fetchval", "hstore_fetchval", "hstore,25", 25, true},
	{HstoreSliceSlot, "slice_array", "hstore_slice_to_array", "hstore,1009", 1009, true},
	{HstoreExistsSlot, "exist", "hstore_exists", "hstore,25", 16, true},
	{HstoreDefinedSlot, "defined", "hstore_defined", "hstore,25", 16, true},
}

func hstoreRoutineAt(slot int) (hstoreRoutine, bool) {
	if slot >= HstoreComparisonProcedureStart && slot < HstoreComparisonOperatorStart {
		kind := slot - HstoreComparisonProcedureStart
		name, _, result := HstoreComparisonDeclaration(kind, 0)
		signature := "hstore,hstore"
		if kind == 7 {
			signature = "hstore"
		} else if kind == 8 {
			signature = "hstore,20"
		}
		return hstoreRoutine{slot, name, name, signature, sqltype.ID(result), true}, true
	}
	for _, r := range hstoreRoutines {
		if r.slot == slot {
			return r, true
		}
	}
	return hstoreRoutine{}, false
}
func (r hstoreRoutine) declaration(base sqltype.ID) (catalog.ProcedureSignature, ids.OID) {
	signature := strings.ReplaceAll(r.signature, "hstore", string(HstoreSignature(base)))
	result := r.result
	if result == 0 {
		result = base
	}
	return catalog.ProcedureSignature(signature), ids.OID(result)
}

type hstoreOperator struct {
	slot, procedure int
	name            string
	right, result   sqltype.ID
}

var hstoreOperators = [...]hstoreOperator{
	{HstoreArrayOperatorSlot, HstoreArrayProcedureSlot, "%%", 0, 1009},
	{HstoreMatrixOperatorSlot, HstoreMatrixProcedureSlot, "%#", 0, 1009},
	{HstoreFetchOperatorSlot, HstoreFetchSlot, "->", 25, 25},
	{HstoreSliceOperatorSlot, HstoreSliceSlot, "->", 1009, 1009},
}

func HstoreOperatorSlot(slot int) bool {
	if slot >= HstoreComparisonOperatorStart && slot < HstoreBtreeFamilySlot {
		return true
	}
	for _, r := range hstoreOperators {
		if r.slot == slot {
			return true
		}
	}
	return false
}
func (r hstoreOperator) operands(base sqltype.ID) (ids.OID, ids.OID) {
	if r.slot == HstoreArrayOperatorSlot || r.slot == HstoreMatrixOperatorSlot {
		return 0, ids.OID(base)
	}
	return ids.OID(base), ids.OID(r.right)
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
	for _, recipe := range hstoreRoutines {
		proc, ok := v.Procedure(ext.Members[recipe.slot])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for _, recipe := range hstoreOperators {
		if err := validateHstoreOperator(v, ext, base, recipe.slot); err != nil {
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

func validateHstoreOperator(v catalog.Read, ext *catalog.Extension, base *catalog.Type, slot int) error {
	for _, recipe := range hstoreOperators {
		if recipe.slot != slot {
			continue
		}
		left, right := recipe.operands(base.SQLType.ID())
		op, ok := v.Operator(ext.Members[slot])
		if !ok || op.Address() != ext.Members[slot] || op.Namespace() != ext.TargetNamespace || op.Name() != recipe.name || op.Extension != ext.Address() || op.Left != left || op.Right != right || op.Result != ids.OID(recipe.result) || op.Function != ext.Members[recipe.procedure].ObjectID || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge {
			return fmt.Errorf("stuntgres: invalid hstore operator")
		}
		return ValidateProviderHeader(v, op, ids.OperatorClassID)
	}
	return fmt.Errorf("stuntgres: invalid hstore operator slot")
}
