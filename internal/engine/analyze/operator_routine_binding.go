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
	if d.IsArray() {
		return 2277 // Released ANYARRAY declaration after actual capability admission.
	}
	id := d.ID()
	if formal := rangeRoutineFormalID(id); formal != 0 {
		return formal
	}
	if id == sqltype.Regclass || id == sqltype.Regtype || id == sqltype.Regrole || sqltype.IsRoutineAlias(id) || sqltype.IsOperatorAlias(id) || sqltype.IsRegObjectAlias(id) || sqltype.IsRegTS(id) {
		return sqltype.Oid
	}
	return id
}

// Normalize only the stock physical providers admitted by existing range
// authority. Other operands remain scalar subtypes, not released-only ranges.
func rangeRoutineFormalID(id sqltype.ID) sqltype.ID {
	switch id {
	case sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange,
		sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange:
		return 3831 // ANYRANGE
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		return 4537 // ANYMULTIRANGE
	}
	return 0
}

func (b *Binder) selectedComparisonRoutineInfo(info expr.Info, procedure *catalog.Procedure) (expr.Info, ids.ObjectRef, error) {
	info.RoutineReference = procedure
	var ref ids.ObjectRef
	if procedure.Implementation == extension.StockCube {
		view := b.BindingCatalog()
		if view.Owner() == nil {
			return expr.Info{}, ref, fmt.Errorf("stuntgres: missing installed comparison authority")
		}
		ref = view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	}
	return info, ref, nil
}

// Only the installed stock manifest grants shared comparison consumers this
// dynamic authority. Arbitrary user operators remain unsupported here.
func (b *Binder) cubeOperatorProcedure(name string, typ *sqltype.Descriptor) (*catalog.Procedure, error) {
	view := b.BindingCatalog()
	base, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || base.SQLType != typ {
		return nil, fmt.Errorf("stuntgres: foreign cube comparison type")
	}
	probe := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectNamespace: base.Namespace(), ObjectName: name, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID())}
	operator, ok := view.Operator(view.NameAddress(catalog.DescriptorNameKey(probe)))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing installed cube operator")
	}
	procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
	if !ok || extension.CubeProviderOf(procedure) != extension.StockCube || operator.Extension != base.Extension {
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
