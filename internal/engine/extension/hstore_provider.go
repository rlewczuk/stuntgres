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
	HstoreExistsAllSlot
	HstoreExistsAnySlot
	HstoreContainsSlot
	HstoreContainedSlot
	HstoreKeysSlot
	HstoreValuesSlot
	HstoreExistsOperatorSlot
	HstoreExistsAllOperatorSlot
	HstoreExistsAnyOperatorSlot
	HstoreContainsOperatorSlot
	HstoreContainedOperatorSlot
	HstoreRecordConstructorSlot
	HstorePopulateRecordSlot
	HstorePopulateRecordOperatorSlot
	HstoreSkeysSlot
	HstoreSvalsSlot
	HstoreEachSlot
	HstoreConcatSlot
	HstoreDeleteSlot
	HstoreDeleteArraySlot
	HstoreDeleteHstoreSlot
	HstoreUpdateSliceSlot
	HstoreConcatOperatorSlot
	HstoreDeleteOperatorSlot
	HstoreDeleteArrayOperatorSlot
	HstoreDeleteHstoreOperatorSlot
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
	{HstoreExistsAllSlot, "exists_all", "hstore_exists_all", "hstore,1009", 16, true},
	{HstoreExistsAnySlot, "exists_any", "hstore_exists_any", "hstore,1009", 16, true},
	{HstoreContainsSlot, "hs_contains", "hstore_contains", "hstore,hstore", 16, true},
	{HstoreContainedSlot, "hs_contained", "hstore_contained", "hstore,hstore", 16, true},
	{HstoreKeysSlot, "akeys", "hstore_akeys", "hstore", 1009, true},
	{HstoreValuesSlot, "avals", "hstore_avals", "hstore", 1009, true},
	{HstoreRecordConstructorSlot, "hstore", "hstore_from_record", "2249", 0, false},
	{HstorePopulateRecordSlot, "populate_record", "hstore_populate_record", "2283,hstore", 2283, false},
	{HstoreSkeysSlot, "skeys", "hstore_skeys", "hstore", 25, true},
	{HstoreSvalsSlot, "svals", "hstore_svals", "hstore", 25, true},
	{HstoreEachSlot, "each", "hstore_each", "hstore", 2249, true},
	{HstoreConcatSlot, "hs_concat", "hstore_concat", "hstore,hstore", 0, true},
	{HstoreDeleteSlot, "delete", "hstore_delete", "hstore,25", 0, true},
	{HstoreDeleteArraySlot, "delete", "hstore_delete_array", "hstore,1009", 0, true},
	{HstoreDeleteHstoreSlot, "delete", "hstore_delete_hstore", "hstore,hstore", 0, true},
	{HstoreUpdateSliceSlot, "slice", "hstore_slice_to_hstore", "hstore,1009", 0, true},
}

var hstoreEachOutputs = catalog.NewRoutineOutputs([]ids.OID{25, 25}, "hs", "key", "value")

func hstoreRoutineDeclaration(slot int) catalog.InstalledRoutineDeclaration {
	switch slot {
	case HstoreSkeysSlot, HstoreSvalsSlot:
		return catalog.InstalledRoutineDeclaration{ReturnsSet: true, Rows: 1000, Parallel: 's'}
	case HstoreEachSlot:
		return catalog.InstalledRoutineDeclaration{ReturnsSet: true, Rows: 1000, Parallel: 's', ArgumentNames: [4]string{"hs"}, Outputs: hstoreEachOutputs}
	default:
		return catalog.InstalledRoutineDeclaration{}
	}
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
	slot, procedure   int
	name              string
	right, result     sqltype.ID
	rightHstore       bool
	commutator        int
	restriction, join ids.OID
}

var hstoreOperators = [...]hstoreOperator{
	{slot: HstoreArrayOperatorSlot, procedure: HstoreArrayProcedureSlot, name: "%%", result: 1009},
	{slot: HstoreMatrixOperatorSlot, procedure: HstoreMatrixProcedureSlot, name: "%#", result: 1009},
	{slot: HstoreFetchOperatorSlot, procedure: HstoreFetchSlot, name: "->", right: 25, result: 25},
	{slot: HstoreSliceOperatorSlot, procedure: HstoreSliceSlot, name: "->", right: 1009, result: 1009},
	{slot: HstoreExistsOperatorSlot, procedure: HstoreExistsSlot, name: "?", right: 25, result: 16, restriction: 5040, join: 5041},
	{slot: HstoreExistsAllOperatorSlot, procedure: HstoreExistsAllSlot, name: "?&", right: 1009, result: 16, restriction: 5040, join: 5041},
	{slot: HstoreExistsAnyOperatorSlot, procedure: HstoreExistsAnySlot, name: "?|", right: 1009, result: 16, restriction: 5040, join: 5041},
	{slot: HstoreContainsOperatorSlot, procedure: HstoreContainsSlot, name: "@>", rightHstore: true, result: 16, commutator: HstoreContainedOperatorSlot, restriction: 5040, join: 5041},
	{slot: HstoreContainedOperatorSlot, procedure: HstoreContainedSlot, name: "<@", rightHstore: true, result: 16, commutator: HstoreContainsOperatorSlot, restriction: 5040, join: 5041},
	{slot: HstorePopulateRecordOperatorSlot, procedure: HstorePopulateRecordSlot, name: "#=", rightHstore: true, result: 2283},
	{slot: HstoreConcatOperatorSlot, procedure: HstoreConcatSlot, name: "||", rightHstore: true},
	{slot: HstoreDeleteOperatorSlot, procedure: HstoreDeleteSlot, name: "-", right: 25},
	{slot: HstoreDeleteArrayOperatorSlot, procedure: HstoreDeleteArraySlot, name: "-", right: 1009},
	{slot: HstoreDeleteHstoreOperatorSlot, procedure: HstoreDeleteHstoreSlot, name: "-", rightHstore: true},
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
	if r.slot == HstorePopulateRecordOperatorSlot {
		return 2283, ids.OID(base)
	}
	if r.slot == HstoreArrayOperatorSlot || r.slot == HstoreMatrixOperatorSlot {
		return 0, ids.OID(base)
	}
	if r.rightHstore {
		return ids.OID(base), ids.OID(base)
	}
	return ids.OID(base), ids.OID(r.right)
}

func (r hstoreOperator) resultOID(base sqltype.ID) ids.OID {
	if r.result == 0 {
		return ids.OID(base)
	}
	return ids.OID(r.result)
}

func (r hstoreOperator) commutatorOID(ext *catalog.Extension) ids.OID {
	if r.commutator == 0 {
		return 0
	}
	return ext.Members[r.commutator].ObjectID
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
		if !ok || op.Address() != ext.Members[slot] || op.Name() != recipe.name || op.Extension != ext.Address() || op.Left != left || op.Right != right || op.Result != recipe.resultOID(base.SQLType.ID()) || op.Function != ext.Members[recipe.procedure].ObjectID || op.Commutator != recipe.commutatorOID(ext) || op.Negator != 0 || op.Restriction != recipe.restriction || op.Join != recipe.join || op.Hash || op.Merge {
			return fmt.Errorf("stuntgres: invalid hstore operator")
		}
		return ValidateProviderHeader(v, op, ids.OperatorClassID)
	}
	return fmt.Errorf("stuntgres: invalid hstore operator slot")
}

// HstoreRelocatableOperator authenticates recipe operators that may move
// independently of installation. Comparison operators retain their existing
// family/namespace contract. This does not replace full operator validation.
func HstoreRelocatableOperator(v catalog.Read, ext *catalog.Extension, address ids.ObjectAddress) bool {
	if ext == nil || ext.Name() != "hstore" || address.ClassID != ids.OperatorClassID || ext.Version != StockHstore.control.Version || v.Owner().Extensions[ext.Name()] != StockHstore || len(ext.Members) != HstoreMemberCount || !currentManifest(v, ext) {
		return false
	}
	base, ok := v.Type(ext.Members[HstoreTypeSlot])
	if !ok || base.SQLType == nil {
		return false
	}
	for _, recipe := range hstoreOperators {
		if ext.Members[recipe.slot] == address {
			return validateHstoreOperator(v, ext, base, recipe.slot) == nil
		}
	}
	return false
}
