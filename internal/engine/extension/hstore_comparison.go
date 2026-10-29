package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var HstoreComparisonNames = [...]string{"hstore_eq", "hstore_ne", "hstore_lt", "hstore_le", "hstore_gt", "hstore_ge", "hstore_cmp", "hstore_hash", "hstore_hash_extended"}
var hstoreComparisonOperators = [...]string{"=", "<>", "#<#", "#<=#", "#>#", "#>=#"}

func HstoreComparisonName(name string) bool {
	for _, n := range HstoreComparisonNames {
		if name == n {
			return true
		}
	}
	return false
}

func HstoreComparisonDeclaration(kind int, typ sqltype.ID) (string, catalog.ProcedureSignature, ids.OID) {
	if kind < 0 || kind >= len(HstoreComparisonNames) {
		return "", "", 0
	}
	signature := string(HstoreSignature(typ))
	result := ids.OID(sqltype.Bool)
	switch kind {
	case 7:
		result = ids.OID(sqltype.Int4)
	case 8:
		signature += ",20"
		result = ids.OID(sqltype.Int8)
	default:
		signature += "," + signature
		if kind == 6 {
			result = ids.OID(sqltype.Int4)
		}
	}
	return HstoreComparisonNames[kind], catalog.ProcedureSignature(signature), result
}

func hstoreMemberClass(slot int) ids.OID {
	switch {
	case slot == HstoreTypeSlot:
		return ids.TypeClassID
	case HstoreOperatorSlot(slot):
		return ids.OperatorClassID
	case slot == HstoreJSONBCastSlot:
		return ids.CastClassID
	case slot == HstoreBtreeFamilySlot || slot == HstoreHashFamilySlot:
		return ids.OperatorFamilyClassID
	case slot == HstoreBtreeClassSlot || slot == HstoreHashClassSlot:
		return ids.OperatorClassCatalogID
	default:
		return ids.ProcedureClassID
	}
}

func validateHstoreComparisons(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid hstore comparison manifest") }
	for i := range HstoreComparisonNames {
		p, ok := v.Procedure(ext.Members[HstoreComparisonProcedureStart+i])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, p); err != nil {
			return err
		}
	}
	for i, name := range hstoreComparisonOperators {
		o, ok := v.Operator(ext.Members[HstoreComparisonOperatorStart+i])
		if !ok || o.Name() != name || o.Namespace() != ext.TargetNamespace || o.Extension != ext.Address() || o.Left != ids.OID(base.SQLType.ID()) || o.Right != o.Left || o.Result != 16 || o.Function != ext.Members[HstoreComparisonProcedureStart+i].ObjectID || o.Commutator != ext.Members[HstoreComparisonOperatorStart+cubeCommutators[i]].ObjectID || o.Negator != ext.Members[HstoreComparisonOperatorStart+cubeNegators[i]].ObjectID || o.Restriction != cubeRestrictions[i] || o.Join != cubeJoins[i] || o.Hash != (i == 0) || o.Merge != (i == 0) {
			return invalid()
		}
		if err := ValidateProviderHeader(v, o, ids.OperatorClassID); err != nil {
			return err
		}
	}
	for _, hash := range []bool{false, true} {
		familySlot, classSlot, method, name := HstoreBtreeFamilySlot, HstoreBtreeClassSlot, ids.OID(403), "btree_hstore_ops"
		if hash {
			familySlot, classSlot, method, name = HstoreHashFamilySlot, HstoreHashClassSlot, 405, "hash_hstore_ops"
		}
		f, ok := v.OperatorFamily(ext.Members[familySlot])
		if !ok || f.Name() != name || f.Method != method || f.Extension != ext.Address() || f.Namespace() != ext.TargetNamespace {
			return invalid()
		}
		if err := ValidateProviderHeader(v, f, ids.OperatorFamilyClassID); err != nil {
			return err
		}
		c, ok := v.OperatorClass(ext.Members[classSlot].ObjectID)
		if !ok || c.Name() != name || c.Method != method || c.Family != f.Address().ObjectID || c.InputType != ids.OID(base.SQLType.ID()) || c.StorageType != 0 || !c.Default || c.Extension != ext.Address() || c.Namespace() != ext.TargetNamespace {
			return invalid()
		}
		if err := ValidateProviderHeader(v, c, ids.OperatorClassCatalogID); err != nil {
			return err
		}
		if err := validateHstoreClassBindings(v, c, ext.Members); err != nil {
			return err
		}
	}
	return nil
}

// Validate the actual AMOP/AMPROC graph of a surviving released class without
// making current extension membership order or placement part of its ABI.
func validateHstoreClassBindings(v catalog.Read, class *catalog.OperatorClass, members []ids.ObjectAddress) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed hstore class binding") }
	if len(members) != HstoreMemberCount {
		return invalid()
	}
	method, classSlot := ids.OID(403), HstoreBtreeClassSlot
	strategies := cubeStrategies[:]
	supportKinds := [2]int{6}
	supportCount := 1
	if class.Address() == members[HstoreHashClassSlot] {
		method, classSlot = 405, HstoreHashClassSlot
		strategies = strategies[:1]
		supportKinds, supportCount = [2]int{7, 8}, 2
	}
	if class.Address() != members[classSlot] || class.Method != method {
		return invalid()
	}
	for index, kind := range strategies {
		if method == 405 {
			kind = 0
		}
		member, ok := v.FamilyOperator(class.Family, class.InputType, class.InputType, int16(index+1), 's')
		if !ok || member.Operator != members[HstoreComparisonOperatorStart+kind].ObjectID || member.Method != method || member.SortFamily != 0 || member.InternalOwner != class.Address() || member.Revision() == 0 {
			return invalid()
		}
	}
	for index := range supportCount {
		member, ok := v.FamilySupport(class.Family, class.InputType, class.InputType, int16(index+1))
		if !ok || member.Function != members[HstoreComparisonProcedureStart+supportKinds[index]].ObjectID || member.InternalOwner != class.Address() || !member.CatalogVisible || member.Revision() == 0 {
			return invalid()
		}
	}
	return nil
}
