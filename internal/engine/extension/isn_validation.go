package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func validateIsnDeclarations(v catalog.Read, ext *catalog.Extension) error {
	for slot := IsnComparisonStart; slot < IsnMemberCount; slot++ {
		d := v.Get(ext.Members[slot])
		if d == nil || d.Namespace() != ext.TargetNamespace && isnMemberClass(slot) != ids.CastClassID {
			return InvalidProviderDescriptor()
		}
		if err := ValidateProviderHeader(v, d, isnMemberClass(slot)); err != nil {
			return err
		}
		switch d := d.(type) {
		case *catalog.Procedure:
			name, _, signature, result, display, err := isnRoutineDeclaration(v, ext.Members, slot)
			volatility, parallel := IsnRoutineProperties(slot)
			if err != nil || d.Name() != name || d.Signature != signature || d.Result != result || d.Display != display || d.Implementation != StockIsn || d.Installed != (catalog.InstalledRoutineDeclaration{Parallel: parallel}) || !d.Strict || d.Volatility != volatility || d.RoutineKind != catalog.FunctionRoutine || d.Language != (ids.ObjectAddress{}) || d.SQL != nil || d.Builtin != nil || d.Foreign != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Aggregate != nil || d.SecurityDefiner || d.Extension != ext.Address() {
				return InvalidProviderDescriptor()
			}
		case *catalog.Operator:
			i, op := (slot-IsnOperatorStart)/6, (slot-IsnOperatorStart)%6
			pair := isnPairs[i]
			if d.Name() != cubeOperatorNames[op] || d.Left != ext.Members[isnRecipes[pair[0]].slot].ObjectID || d.Right != ext.Members[isnRecipes[pair[1]].slot].ObjectID || d.Result != 16 || d.Function != ext.Members[IsnComparisonStart+i*6+op].ObjectID || d.Commutator != ext.Members[IsnOperatorStart+isnPairIndex(pair[1], pair[0])*6+cubeCommutators[op]].ObjectID || d.Negator != ext.Members[IsnOperatorStart+i*6+cubeNegators[op]].ObjectID || d.Restriction != cubeRestrictions[op] || d.Join != cubeJoins[op] || d.Hash != (op == 0) || d.Merge != (op == 0) {
				return InvalidProviderDescriptor()
			}
		case *catalog.OperatorFamily:
			if d.Name() != "isn_ops" || d.Method != 403 {
				return InvalidProviderDescriptor()
			}
		case *catalog.OperatorClass:
			role := slot - IsnClassStart
			if d.Name() != isnRecipes[role].name+"_ops" || d.Method != 403 || d.Family != ext.Members[IsnFamilySlot].ObjectID || d.InputType != ext.Members[isnRecipes[role].slot].ObjectID || !d.Default || d.StorageType != 0 {
				return InvalidProviderDescriptor()
			}
			if err := validateIsnClassRows(v, d, ext.Members, role); err != nil {
				return err
			}
		case *catalog.Cast:
			pair := isnCastPairs[slot-IsnCastStart]
			context, method, function := "a", "b", ids.ObjectAddress{}
			if pair[0] == 0 {
				context, method, function = "e", "f", ext.Members[IsnConversionStart+pair[1]-1]
			}
			if d.Source != sqltype.ID(ext.Members[isnRecipes[pair[0]].slot].ObjectID) || d.Target != sqltype.ID(ext.Members[isnRecipes[pair[1]].slot].ObjectID) || d.Context != context || d.Method != method || d.Function != function {
				return InvalidProviderDescriptor()
			}
		default:
			return InvalidProviderDescriptor()
		}
	}
	return nil
}

func validateIsnClassRows(v catalog.Read, class *catalog.OperatorClass, members []ids.ObjectAddress, role int) error {
	for i, op := range cubeStrategies {
		m, ok := v.FamilyOperator(class.Family, class.InputType, class.InputType, int16(i+1), 's')
		if !ok || m.Operator != members[IsnOperatorStart+isnPairIndex(role, role)*6+op].ObjectID || m.Method != 403 || m.SortFamily != 0 || m.InternalOwner != class.Address() || m.Revision() == 0 {
			return InvalidProviderDescriptor()
		}
		if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodOperatorClassID}).Validate(v, m); err != nil {
			return err
		}
	}
	m, ok := v.FamilySupport(class.Family, class.InputType, class.InputType, 1)
	if !ok || m.Function != members[IsnSupportStart+role].ObjectID || m.InternalOwner != class.Address() || !m.CatalogVisible || m.Revision() == 0 {
		return InvalidProviderDescriptor()
	}
	return (catalog.OperatorFamilyProvider{Class: ids.AccessMethodProcedureClassID}).Validate(v, m)
}

func ValidateIsnClassBindings(v catalog.Read, class *catalog.OperatorClass) error {
	installation, ok := catalog.OriginInstallation(class)
	if !ok || installation.Implementation != StockIsn || len(installation.Members) != IsnMemberCount {
		return InvalidProviderDescriptor()
	}
	role := class.Origin.Declaration - IsnClassStart
	if role < 0 || role >= len(isnRecipes) {
		return InvalidProviderDescriptor()
	}
	if err := catalog.ValidateInstalledClassOrigin(v, class); err != nil {
		return err
	}
	family, ok := v.OperatorFamily(installation.Members[IsnFamilySlot])
	if !ok || !catalog.SameInstallation(family, installation) || class.Family != family.Address().ObjectID || class.Method != 403 || family.Method != 403 {
		return InvalidProviderDescriptor()
	}
	if err := catalog.ValidateInstalledClassOrigin(v, family); err != nil {
		return err
	}
	t, ok := v.Type(installation.Members[isnRecipes[role].slot])
	if !ok || class.InputType != ids.OID(t.SQLType.ID()) {
		return InvalidProviderDescriptor()
	}
	if _, err := isnTypeRole(v, t, installation); err != nil {
		return err
	}
	return validateIsnClassRows(v, class, installation.Members, role)
}
