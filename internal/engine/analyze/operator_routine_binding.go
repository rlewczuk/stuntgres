package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func (b *Binder) builtinOperatorRoutineInfo(info expr.Info, name string, left, right sqltype.ID) (expr.Info, error) {
	if name == "!=" {
		name = "<>"
	}
	declaration := routine.OperatorRoutine(name, ids.OID(left), ids.OID(right))
	if declaration == nil {
		return expr.Info{}, fmt.Errorf("stuntgres: missing successful operator routine association for %s (%d,%d)", name, left, right)
	}
	return b.selectedBuiltinRoutineInfo(info, declaration)
}

// Operands are already the instantiated targets selected by ResolveComparison.
// This lookup records the released routine identity, never a second resolver or
// physical source-pair capability gate.
func (b *Binder) comparisonRoutineDeclaration(name string, left, right *sqltype.Descriptor) (*catalog.Procedure, sqltype.EqualitySignature, error) {
	if name == "!=" {
		name = "<>"
	}
	if left.IsCube() && left == right {
		procedure, err := b.cubeOperatorProcedure(name, left)
		return procedure, sqltype.EqualitySignature{Left: left.ID(), Right: right.ID()}, err
	}
	if left.IsHstore() && left == right {
		procedure, err := b.hstoreOperatorProcedure(name, left)
		return procedure, sqltype.EqualitySignature{Left: left.ID(), Right: right.ID()}, err
	}
	if left.IsLtree() && left == right {
		procedure,err:=b.ltreeOperatorProcedure(name,left)
		return procedure,sqltype.EqualitySignature{Left:left.ID(),Right:right.ID()},err
	}
	formal := sqltype.EqualitySignature{Left: comparisonFormalID(left.BaseType()), Right: comparisonFormalID(right.BaseType())}
	declaration := routine.OperatorRoutine(name, ids.OID(formal.Left), ids.OID(formal.Right))
	if declaration == nil {
		return nil, sqltype.EqualitySignature{}, fmt.Errorf("stuntgres: missing successful comparison routine association for %s (%d,%d)", name, formal.Left, formal.Right)
	}
	procedure, _, err := b.captureBuiltinRoutineDeclaration(declaration)
	return procedure, formal, err
}

func comparisonFormalID(d *sqltype.Descriptor) sqltype.ID {
	if d.IsComposite() || d.IsDeferredRecord() {
		return 2249 // RECORD, with occurrence-owned concrete field authority.
	}
	if d.IsEnum() {
		return 3500 // Released ANYENUM declaration; no concrete enum promotion.
	}
	if d.IsCatalogVector() && d.ID() == sqltype.OidVector {
		return sqltype.OidVector
	}
	if d.IsArray() {
		return 2277 // Released ANYARRAY declaration after actual capability admission.
	}
	id := d.ID()
	if formal := rangeRoutineFormalID(d); formal != 0 {
		return formal
	}
	if id == sqltype.Regclass || id == sqltype.Regtype || id == sqltype.Regrole || sqltype.IsRoutineAlias(id) || sqltype.IsOperatorAlias(id) || sqltype.IsRegObjectAlias(id) || sqltype.IsRegTS(id) {
		return sqltype.Oid
	}
	return id
}

// Formal range identities follow admitted descriptor families, not allocated OIDs.
func rangeRoutineFormalID(d *sqltype.Descriptor) sqltype.ID {
	if d.IsRange() {
		return 3831
	}
	if d.IsMultirange() {
		return 4537
	}
	return 0
}

func (b *Binder) selectedComparisonRoutineInfo(info expr.Info, procedure *catalog.Procedure) (expr.Info, ids.ObjectRef, error) {
	info.RoutineReference = procedure
	var ref ids.ObjectRef
	if procedure.Implementation == extension.StockCube || procedure.Implementation == extension.StockHstore || procedure.Implementation == extension.StockLtree {
		view := b.BindingCatalog()
		if view.Owner() == nil {
			return expr.Info{}, ref, fmt.Errorf("stuntgres: missing installed comparison authority")
		}
		ref = view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	}
	return info, ref, nil
}

// Only a genuine installed declaration grants comparison consumers this
// authority. Mutable namespace, label and membership never select its provider.
func (b *Binder) cubeOperatorProcedure(name string, typ *sqltype.Descriptor) (*catalog.Procedure, error) {
	view := b.BindingCatalog()
	base, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || base.SQLType != typ {
		return nil, fmt.Errorf("stuntgres: foreign cube comparison type")
	}
	installation, valid := catalog.OriginInstallation(base)
	if !valid || installation.Implementation != extension.StockCube {
		return nil, fmt.Errorf("stuntgres: missing cube declaration origin")
	}
	var operator *catalog.Operator
	for i, declaration := range installation.Declarations {
		canonical, ok := declaration.(*catalog.Operator)
		if ok && canonical.Name() == name && canonical.Left == ids.OID(typ.ID()) && canonical.Right == ids.OID(typ.ID()) {
			operator, _ = view.Operator(installation.Members[i])
			break
		}
	}
	if operator == nil || !catalog.SameInstallation(operator, installation) {
		return nil, fmt.Errorf("stuntgres: missing installed cube operator")
	}
	procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
	if !ok || extension.CubeProviderOf(procedure) != extension.StockCube || !catalog.SameInstallation(procedure, installation) {
		return nil, fmt.Errorf("stuntgres: invalid installed cube operator")
	}
	if err := extension.StockCube.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	for _, d := range []catalog.Descriptor{operator, procedure} {
		ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
		b.dependency(ref, d.Revision())
	}
	return procedure, nil
}

func (b *Binder) ltreeOperatorProcedure(name string, typ *sqltype.Descriptor) (*catalog.Procedure, error) {
	view := b.BindingCatalog()
	base, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || base.SQLType != typ {
		return nil, fmt.Errorf("stuntgres: foreign ltree comparison type")
	}
	installation, valid := catalog.OriginInstallation(base)
	if !valid || installation.Implementation != extension.StockLtree {
		return nil, fmt.Errorf("stuntgres: missing ltree declaration origin")
	}
	var operator *catalog.Operator
	for i, declaration := range installation.Declarations {
		canonical, ok := declaration.(*catalog.Operator)
		if ok && canonical.Name() == name && canonical.Left == ids.OID(typ.ID()) && canonical.Right == ids.OID(typ.ID()) {
			operator, _ = view.Operator(installation.Members[i])
			break
		}
	}
	if operator == nil || !catalog.SameInstallation(operator, installation) {
		return nil, fmt.Errorf("stuntgres: missing installed ltree operator")
	}
	if err := catalog.ValidateInstalledClassOrigin(view, operator); err != nil {
		return nil, err
	}
	if err := catalog.ValidateExtensionMembership(view, operator); err != nil {
		return nil, err
	}
	procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
	if !ok || extension.LtreeProviderOf(procedure) != extension.StockLtree || !catalog.SameInstallation(procedure, installation) {
		return nil, fmt.Errorf("stuntgres: invalid installed ltree operator")
	}
	if err := extension.StockLtree.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	for _, d := range []catalog.Descriptor{operator, procedure} {
		b.dependency(view.Owner().Domain.Ref(view.Epoch(), d.Address()), d.Revision())
	}
	return procedure, nil
}
