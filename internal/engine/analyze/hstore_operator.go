package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (b *Binder) hstoreOperatorProcedure(name string, typ *sqltype.Descriptor) (*catalog.Procedure, error) {
	view := b.BindingCatalog()
	base, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || base.SQLType != typ {
		return nil, fmt.Errorf("stuntgres: foreign hstore comparison type")
	}
	installation, ok := catalog.OriginInstallation(base)
	if !ok || installation.Implementation != extension.StockHstore || len(installation.Members) != extension.HstoreMemberCount {
		return nil, fmt.Errorf("stuntgres: missing hstore comparison origin")
	}
	var operator *catalog.Operator
	for slot := extension.HstoreComparisonOperatorStart; slot < extension.HstoreBtreeFamilySlot; slot++ {
		declaration, ok := installation.Declarations[slot].(*catalog.Operator)
		if ok && declaration.Name() == name {
			operator, _ = view.Operator(installation.Members[slot])
			break
		}
	}
	if operator == nil {
		return nil, fmt.Errorf("stuntgres: missing installed hstore operator")
	}
	p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
	if !ok || extension.HstoreProviderOf(p) != extension.StockHstore || !catalog.SameInstallation(p, installation) || !catalog.SameInstallation(operator, installation) {
		return nil, fmt.Errorf("stuntgres: foreign installed hstore operator")
	}
	if err := (extension.CatalogProvider{Class: ids.OperatorClassID}).Validate(view, operator); err != nil {
		return nil, err
	}
	if err := extension.StockHstore.ValidateProcedure(view, p); err != nil {
		return nil, err
	}
	for _, d := range []catalog.Descriptor{operator, p} {
		b.dependency(view.Owner().Domain.Ref(view.Epoch(), d.Address()), d.Revision())
	}
	return p, nil
}
