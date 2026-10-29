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
		operators, supports := 0, 0
		for _, descriptor := range v.Descriptors() {
			switch member := descriptor.(type) {
			case *catalog.OperatorFamilyOperator:
				if member.Family == c.Family {
					operators++
				}
			case *catalog.OperatorFamilySupport:
				if member.Family == c.Family {
					supports++
				}
			}
		}
		if operators != len(strategies) || supports != len(supportKinds) {
			return invalid()
		}
	}
	return nil
}

func ltreeSupportOwner(c *catalog.OperatorClass, hash bool, index int) ids.ObjectAddress {
	if hash && index == 1 {
		return ids.ObjectAddress{}
	}
	return c.Address()
}

// ValidateLtreeClassBindings checks the surviving released AMOP/AMPROC graph.
// Current names and extension membership are not positions in that graph.
func ValidateLtreeClassBindings(v catalog.Read, class *catalog.OperatorClass) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed ltree class binding") }
	installation, ok := catalog.OriginInstallation(class)
	if !ok || installation.Implementation != StockLtree || len(installation.Members) != LtreeMemberCount {
		return invalid()
	}
	if err := catalog.ValidateInstalledClassOrigin(v, class); err != nil {
		return err
	}
	members := installation.Members
	method, classSlot := ids.OID(403), LtreeBtreeClassSlot
	strategies := cubeStrategies[:]
	supportKinds, supportCount := [2]int{6}, 1
	hash := class.Address() == members[LtreeHashClassSlot]
	if hash {
		method, classSlot = 405, LtreeHashClassSlot
		strategies = strategies[:1]
		supportKinds, supportCount = [2]int{7, 8}, 2
	}
	if class.Address() != members[classSlot] || class.Method != method {
		return invalid()
	}
	family, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: class.Family})
	if !ok || !catalog.SameInstallation(family, installation) {
		return invalid()
	}
	if err := (catalog.OperatorFamilyProvider{Class: ids.OperatorFamilyClassID}).Validate(v, family); err != nil {
		return err
	}
	for index, kind := range strategies {
		if hash {
			kind = 0
		}
		member, ok := v.FamilyOperator(class.Family, class.InputType, class.InputType, int16(index+1), 's')
		if !ok || member.Operator != members[LtreeComparisonOperatorStart+kind].ObjectID || member.Method != method || member.SortFamily != 0 || member.InternalOwner != class.Address() || member.Revision() == 0 {
			return invalid()
		}
		if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodOperatorClassID}).Validate(v, member); err != nil {
			return err
		}
		operator, ok := v.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
		if !ok || !catalog.SameInstallation(operator, installation) {
			return invalid()
		}
		if err := (CatalogProvider{Class: ids.OperatorClassID}).Validate(v, operator); err != nil {
			return err
		}
	}
	for index := range supportCount {
		member, ok := v.FamilySupport(class.Family, class.InputType, class.InputType, int16(index+1))
		if !ok || member.Function != members[LtreeComparisonProcedureStart+supportKinds[index]].ObjectID || member.InternalOwner != ltreeSupportOwner(class, hash, index) || !member.CatalogVisible || member.Revision() == 0 {
			return invalid()
		}
		if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodProcedureClassID}).Validate(v, member); err != nil {
			return err
		}
		procedure, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
		if !ok || !catalog.SameInstallation(procedure, installation) {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, procedure); err != nil {
			return err
		}
	}
	operators, supports := 0, 0
	for _, descriptor := range v.Descriptors() {
		switch member := descriptor.(type) {
		case *catalog.OperatorFamilyOperator:
			if member.Family == class.Family {
				operators++
			}
		case *catalog.OperatorFamilySupport:
			if member.Family == class.Family {
				supports++
			}
		}
	}
	if operators != len(strategies) || supports != supportCount {
		return invalid()
	}
	return nil
}

const (
	LtreeComparisonProcedureStart = LtreeArrayOperatorStart + int(sqltype.LtreeArrayOperationCount)
	LtreeComparisonOperatorStart  = LtreeComparisonProcedureStart + len(LtreeComparisonNames)
	LtreeBtreeFamilySlot          = LtreeComparisonOperatorStart + 6
	LtreeBtreeClassSlot           = LtreeBtreeFamilySlot + 1
	LtreeHashFamilySlot           = LtreeBtreeClassSlot + 1
	LtreeHashClassSlot            = LtreeHashFamilySlot + 1
)
