package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type TablefuncProvider struct{ control catalog.ExtensionControl }

var StockTablefunc = &TablefuncProvider{control: catalog.ExtensionControl{Version: "1.0", Superuser: true, Trusted: true, Relocatable: true, Comment: new("functions that manipulate whole tables, including crosstab")}}

type tablefuncMember struct {
	name, source string
	signature    catalog.ProcedureSignature
	result       ids.OID
	volatility   expr.Volatility
}

// Slot zero is incumbent normal_rand. Appended slots follow the release script.
var tablefuncMembers = [...]tablefuncMember{
	{"normal_rand", "normal_rand", "23,701,701", 701, expr.Volatile},
	{"connectby", "connectby_text", "25,25,25,25,23,25", 2249, expr.Stable},
	{"connectby", "connectby_text", "25,25,25,25,23", 2249, expr.Stable},
	{"connectby", "connectby_text_serial", "25,25,25,25,25,23,25", 2249, expr.Stable},
	{"connectby", "connectby_text_serial", "25,25,25,25,25,23", 2249, expr.Stable},
}

func tablefuncNormalDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000}
}

func (p *TablefuncProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *TablefuncProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func TablefuncProviderOf(p *catalog.Procedure) *TablefuncProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*TablefuncProvider)
	return provider
}

func (p *TablefuncProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}

func (p *TablefuncProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid tablefunc manifest") }
	if p != StockTablefunc || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "tablefunc" || ext.Version != "1.0" || len(ext.Members) != tablefuncMemberCount || v.Owner().Extensions["tablefunc"] != p {
		return invalid()
	}
	for _, slot := range []int{0, 1, 2, 3, 4, 5, 12, 13, 14, 15} {
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok || proc.Extension != ext.Address() || proc.Implementation != p {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for n := 2; n <= 4; n++ {
		slot := tablefuncCrosstabStart + 1 + 2*(n-2)
		typ, ok := v.Type(ext.Members[slot])
		if !ok {
			return invalid()
		}
		array, ok := v.Type(ext.Members[slot+1])
		if !ok {
			return invalid()
		}
		for _, member := range []*catalog.Type{typ, array} {
			if member.SQLType == nil || member.ObjectRevision == 0 || !member.SQLType.Concrete() || member.Dimensions != 0 || len(member.DomainChecks) != 0 || member.DomainNotNull != nil || member.DomainDefault != nil || member.DomainDefaultOwn || member.DomainDefaultText != "" {
				return invalid()
			}
			if err := ValidateProviderHeader(v, member, ids.TypeClassID); err != nil {
				return err
			}
		}
		if typ.Address() != ext.Members[slot] || array.Address() != ext.Members[slot+1] || array.Array != 0 {
			return invalid()
		}
		if typ.Revision() == 0 || array.Revision() == 0 || typ.Extension != ext.Address() || array.Extension != ext.Address() || !typ.SQLType.IsComposite() || !array.SQLType.IsArray() || typ.Array != array.SQLType.ID() || array.SQLType.Element() != typ.SQLType || typ.Owner.ID() == 0 || typ.Owner != v.Owner().Principals.Principal(typ.Owner.ID()) || array.Owner != typ.Owner || array.Namespace() != typ.Namespace() || v.NameAddress(catalog.DescriptorNameKey(typ)) != typ.Address() || v.NameAddress(catalog.DescriptorNameKey(array)) != array.Address() {
			return invalid()
		}
		relation, ok := v.Relation(typ.OwningRelation)
		if !ok || relation.RelationKind != catalog.CompositeRelation || relation.RowType != typ.SQLType.ID() || relation.Owner != typ.Owner || relation.Namespace() != typ.Namespace() || relation.Name() != typ.Name() || array.OwningRelation != relation.Address() || relation.Revision() == 0 || len(relation.Columns) != n+1 || len(relation.Attributes) != n+1 || v.NameAddress(catalog.DescriptorNameKey(relation)) != relation.Address() {
			return invalid()
		}
		if typ.SQLType.CompositeLen() != n+1 {
			return invalid()
		}
		text, ok := v.LookupType(sqltype.Text)
		if !ok {
			return invalid()
		}
		for i, column := range relation.Columns {
			name := tablefuncColumnNames[i]
			member := typ.SQLType.CompositeMember(i)
			if column.Name != name || column.Usage != text.Usage() || !column.Nullable || !column.NullabilityKnown || relation.Attributes[i] != int32(i+1) || member.Name != name || member.Attribute != relation.Attributes[i] || member.Usage != column.Usage {
				return invalid()
			}
		}
	}
	return nil
}
func (p *TablefuncProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}

func ValidateTablefuncIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || TablefuncProviderOf(proc) != StockTablefunc {
		return fmt.Errorf("stuntgres: invalid stored tablefunc identity")
	}
	return StockTablefunc.ValidateProcedure(v, proc)
}

// The three released result types and their arrays remain genuine nominal
// capabilities after membership, spelling or ownership changes. Their current
// backing relation and element generations, not historical owners, are authority.
func validateTablefuncTypeOrigin(v catalog.Read, t *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid installed tablefunc type declaration") }
	installation := t.Origin.Installation
	offset := t.Origin.Declaration - tablefuncCrosstabStart - 1
	if installation.Version != "1.0" || len(installation.Members) != tablefuncMemberCount || offset < 0 || offset >= 6 {
		return invalid()
	}
	slot := tablefuncCrosstabStart + 1 + 2*(offset/2)
	typ, ok := v.Type(installation.Members[slot])
	if !ok {
		return invalid()
	}
	array, ok := v.Type(installation.Members[slot+1])
	if !ok {
		return invalid()
	}
	for index, member := range [2]*catalog.Type{typ, array} {
		if member.SQLType == nil || member.SQLType.ID() != sqltype.ID(member.Address().ObjectID) || !member.SQLType.Concrete() || member.Revision() == 0 || !catalog.SameInstallation(member, installation) || member.Origin.Declaration != slot+index || member.Owner.ID() == 0 || member.Owner != v.Owner().Principals.Principal(member.Owner.ID()) || member.Dimensions != 0 || len(member.DomainChecks) != 0 || member.DomainNotNull != nil || member.DomainDefault != nil || member.DomainDefaultOwn || member.DomainDefaultText != "" {
			return invalid()
		}
		if err := ValidateProviderHeader(v, member, ids.TypeClassID); err != nil {
			return err
		}
		if err := catalog.ValidateExtensionMembership(v, member); err != nil {
			return err
		}
	}
	width := 3 + offset/2
	if !typ.SQLType.IsComposite() || typ.SQLType.CompositeLen() != width || !array.SQLType.IsArray() || array.SQLType.Element() != typ.SQLType || typ.Array != array.SQLType.ID() || array.Array != 0 || array.Owner != typ.Owner || array.Namespace() != typ.Namespace() {
		return invalid()
	}
	relation, ok := v.Relation(typ.OwningRelation)
	if !ok || relation.RelationKind != catalog.CompositeRelation || relation.RowType != typ.SQLType.ID() || relation.Owner != typ.Owner || relation.Namespace() != typ.Namespace() || relation.Name() != typ.Name() || array.OwningRelation != relation.Address() || relation.Revision() == 0 || len(relation.Columns) != width || len(relation.Attributes) != width || v.NameAddress(catalog.DescriptorNameKey(relation)) != relation.Address() {
		return invalid()
	}
	text, ok := v.LookupType(sqltype.Text)
	if !ok {
		return invalid()
	}
	for index, column := range relation.Columns {
		member := typ.SQLType.CompositeMember(index)
		if column.Name != tablefuncColumnNames[index] || column.Usage != text.Usage() || !column.Nullable || !column.NullabilityKnown || relation.Attributes[index] != int32(index+1) || member.Name != column.Name || member.Attribute != relation.Attributes[index] || member.Usage != column.Usage {
			return invalid()
		}
	}
	return nil
}
