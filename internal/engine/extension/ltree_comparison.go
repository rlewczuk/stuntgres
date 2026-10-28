package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var LtreeComparisonNames = [...]string{"ltree_eq", "ltree_ne", "ltree_lt", "ltree_le", "ltree_gt", "ltree_ge", "ltree_cmp", "hash_ltree", "hash_ltree_extended"}
var ltreeComparisonOperators = [...]string{"=", "<>", "<", "<=", ">", ">="}

func LtreeComparisonName(name string) bool {
	for _, n := range LtreeComparisonNames {
		if name == n {
			return true
		}
	}
	return false
}

func LtreeComparisonDeclaration(kind int, typ sqltype.ID) (string, catalog.ProcedureSignature, ids.OID) {
	if kind < 0 || kind >= len(LtreeComparisonNames) {
		return "", "", 0
	}
	signature := string(LquerySignature(typ))
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
	return LtreeComparisonNames[kind], catalog.ProcedureSignature(signature), result
}

func validateLtreeComparisons(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree comparison manifest") }
	for i := range LtreeComparisonNames {
		p, ok := v.Procedure(ext.Members[LtreeComparisonProcedureStart+i])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, p); err != nil {
			return err
		}
	}
	for i, name := range ltreeComparisonOperators {
		o, ok := v.Operator(ext.Members[LtreeComparisonOperatorStart+i])
		if !ok || o.Name() != name || o.Namespace() != ext.TargetNamespace || o.Extension != ext.Address() || o.Left != ids.OID(base.SQLType.ID()) || o.Right != o.Left || o.Result != 16 || o.Function != ext.Members[LtreeComparisonProcedureStart+i].ObjectID || o.Commutator != ext.Members[LtreeComparisonOperatorStart+cubeCommutators[i]].ObjectID || o.Negator != ext.Members[LtreeComparisonOperatorStart+cubeNegators[i]].ObjectID || o.Restriction != cubeRestrictions[i] || o.Join != cubeJoins[i] || o.Hash != (i == 0) || o.Merge != (i == 0) {
			return invalid()
		}
		if err := ValidateProviderHeader(v, o, ids.OperatorClassID); err != nil {
			return err
		}
	}
	for _, hash := range []bool{false, true} {
		familySlot, classSlot, method, name := LtreeBtreeFamilySlot, LtreeBtreeClassSlot, ids.OID(403), "ltree_ops"
		strategies := cubeStrategies[:]
		supportKinds := []int{6}
		if hash {
			familySlot, classSlot, method, name = LtreeHashFamilySlot, LtreeHashClassSlot, 405, "hash_ltree_ops"
			strategies = []int{0}
			supportKinds = []int{7, 8}
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
		for i, op := range strategies {
			m, ok := v.FamilyOperator(c.Family, c.InputType, c.InputType, int16(i+1), 's')
			if !ok || m.Operator != ext.Members[LtreeComparisonOperatorStart+op].ObjectID || m.Method != method || m.SortFamily != 0 || m.InternalOwner != c.Address() || m.Revision() == 0 {
				return invalid()
			}
		}
		for i, kind := range supportKinds {
			m, ok := v.FamilySupport(c.Family, c.InputType, c.InputType, int16(i+1))
			if !ok || m.Function != ext.Members[LtreeComparisonProcedureStart+kind].ObjectID || m.InternalOwner != ltreeSupportOwner(c, hash, i) || !m.CatalogVisible || m.Revision() == 0 {
				return invalid()
			}
		}
		operators,supports:=0,0
		for _,descriptor:=range v.Descriptors(){
			switch member:=descriptor.(type){
			case *catalog.OperatorFamilyOperator:
				if member.Family==c.Family{operators++}
			case *catalog.OperatorFamilySupport:
				if member.Family==c.Family{supports++}
			}
		}
		if operators!=len(strategies)||supports!=len(supportKinds){return invalid()}
	}
	return nil
}

func ltreeSupportOwner(c *catalog.OperatorClass, hash bool, index int) ids.ObjectAddress {
 if hash && index==1 { return ids.ObjectAddress{} }; return c.Address()
}

const (
 LtreeComparisonProcedureStart = LtreeArrayOperatorStart + int(sqltype.LtreeArrayOperationCount)
 LtreeComparisonOperatorStart = LtreeComparisonProcedureStart + len(LtreeComparisonNames)
 LtreeBtreeFamilySlot = LtreeComparisonOperatorStart + 6
 LtreeBtreeClassSlot = LtreeBtreeFamilySlot + 1
 LtreeHashFamilySlot = LtreeBtreeClassSlot + 1
 LtreeHashClassSlot = LtreeHashFamilySlot + 1
 LtreeMemberCount = LtreeHashClassSlot + 1
)
