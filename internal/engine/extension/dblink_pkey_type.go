package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const (
	dblinkPkeyTypeSlot      = 3
	dblinkPkeyArraySlot     = 4
	dblinkPkeyProcedureSlot = 5
	dblinkMemberCount       = 6
	dblinkPkeyTypeRole      = "dblink_pkey_results"
)

func dblinkPkeyDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'r', ReturnsSet: true, Rows: 1000}
}

func validateDblinkTypeOrigin(v catalog.Read, t *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed dblink result type") }
	installation := t.Origin.Installation
	if installation.Provider != "dblink" || installation.Version != "1.2" || len(installation.Members) != dblinkMemberCount || (t.Origin.Declaration != dblinkPkeyTypeSlot && t.Origin.Declaration != dblinkPkeyArraySlot) {
		return invalid()
	}
	typ, ok := v.Type(installation.Members[dblinkPkeyTypeSlot])
	if !ok {
		return invalid()
	}
	array, ok := v.Type(installation.Members[dblinkPkeyArraySlot])
	if !ok {
		return invalid()
	}
	for i, member := range [2]*catalog.Type{typ, array} {
		if member.SQLType == nil || member.SQLType.ID() != sqltype.ID(member.Address().ObjectID) || !member.SQLType.Concrete() || member.Revision() == 0 || !catalog.SameInstallation(member, installation) || member.Origin.Declaration != dblinkPkeyTypeSlot+i || member.Owner.ID() == 0 || member.Owner != v.Owner().Principals.Principal(member.Owner.ID()) || member.Dimensions != 0 || len(member.DomainChecks) != 0 || member.DomainNotNull != nil || member.DomainDefault != nil || member.DomainDefaultOwn || member.DomainDefaultText != "" {
			return invalid()
		}
		if err := ValidateProviderHeader(v, member, ids.TypeClassID); err != nil {
			return err
		}
		if err := catalog.ValidateExtensionMembership(v, member); err != nil {
			return err
		}
	}
	if !typ.SQLType.IsComposite() || typ.SQLType.CompositeLen() != 2 || !array.SQLType.IsArray() || array.SQLType.Element() != typ.SQLType || typ.Array != array.SQLType.ID() || array.Array != 0 || array.Owner != typ.Owner || array.Namespace() != typ.Namespace() {
		return invalid()
	}
	relation, ok := v.Relation(typ.OwningRelation)
	if !ok || relation.RelationKind != catalog.CompositeRelation || relation.RowType != typ.SQLType.ID() || relation.Owner != typ.Owner || relation.Namespace() != typ.Namespace() || relation.Name() != typ.Name() || array.OwningRelation != relation.Address() || relation.Revision() == 0 || len(relation.Columns) != 2 || len(relation.Attributes) != 2 || v.NameAddress(catalog.DescriptorNameKey(relation)) != relation.Address() {
		return invalid()
	}
	for i, id := range [2]sqltype.ID{sqltype.Int4, sqltype.Text} {
		field, ok := v.LookupType(id)
		if !ok {
			return invalid()
		}
		column, member := relation.Columns[i], typ.SQLType.CompositeMember(i)
		name := [2]string{"position", "colname"}[i]
		if column.Name != name || column.Usage != field.Usage() || !column.Nullable || !column.NullabilityKnown || relation.Attributes[i] != int32(i+1) || member.Name != name || member.Attribute != relation.Attributes[i] || member.Usage != column.Usage {
			return invalid()
		}
	}
	return nil
}

func ValidateDblinkGetPkeyCall(v catalog.Read, p *catalog.Procedure, call functions.DblinkGetPkey) error {
	if p == nil || call.RoutineReference != p || call.Arg == nil || call.Arg.ResultUsage().Type == nil || call.Arg.ResultUsage().Type.BaseType().ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid dblink_get_pkey expression")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if DblinkProviderOf(p) != StockDblink || symbol != functions.DblinkGetPkeyName {
		return fmt.Errorf("stuntgres: mismatched dblink_get_pkey declaration")
	}
	typ, ok := v.Type(p.Origin.Installation.Members[dblinkPkeyTypeSlot])
	if !ok || call.ResultUsage() != typ.SQLType.Usage() {
		return fmt.Errorf("stuntgres: invalid nominal dblink_get_pkey result")
	}
	return ValidateTypeOrigin(v, typ)
}
