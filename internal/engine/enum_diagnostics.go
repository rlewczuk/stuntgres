package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) diagnosticTypeName(typ *sqltype.Descriptor) string {
	if typ == nil {
		return "unknown"
	}
	if !typ.IsUserDefined() {
		return typ.DiagnosticName()
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return typ.DiagnosticName()
	}
	descriptor, ok := auth.catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}].(*typeDescriptor)
	if !ok {
		return typ.DiagnosticName()
	}
	path, err := b.engine.referencePath(auth)
	if err != nil {
		return typ.DiagnosticName()
	}
	return typeDiagnosticNameAt(auth.catalog, descriptor, path)
}

// captureTypeDiagnosticName freezes input naming against the same catalog and
// reference path used for binding. Recursive codecs consult it only on failure.
func (e *Engine) captureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	catalog := auth.catalog
	return func(id sqltype.ID) string {
		descriptor, ok := catalog.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}].(*typeDescriptor)
		if !ok {
			return ""
		}
		return typeDiagnosticNameAt(catalog, descriptor, path)
	}, nil
}

func (b *expressionBinder) captureTypeDiagnosticInput(typ *sqltype.Descriptor) error {
	if b.input.TypeDiagnosticName != nil || !hasEnumInput(typ) {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	b.input.TypeDiagnosticName, err = b.engine.captureTypeDiagnosticName(auth)
	return err
}

func hasEnumInput(typ *sqltype.Descriptor) bool {
	if typ == nil {
		return false
	}
	typ = typ.BaseType()
	if typ.IsEnum() {
		return true
	}
	if typ.IsArray() {
		return hasEnumInput(typ.Element())
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if hasEnumInput(typ.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}

// Retained write expressions are not rebound with the consuming statement.
// Inspect their admitted IO coercions using the same lexical child walker.
func (b *expressionBinder) captureRetainedWriteInput(defaults []BoundExpr, generations map[int32]columnGeneration) error {
	if b.input.TypeDiagnosticName != nil {
		return nil
	}
	var failure error
	visit := func(node BoundExpr) {
		if failure != nil || b.input.TypeDiagnosticName != nil {
			return
		}
		if cast, ok := node.(coercionExpression); ok && sqltype.IsCharacter(cast.child.ResultUsage().Type.BaseType().ID()) {
			failure = b.captureTypeDiagnosticInput(cast.ResultUsage().Type)
		}
	}
	for _, node := range defaults {
		walkBoundExpression(node, visit)
		if failure != nil || b.input.TypeDiagnosticName != nil {
			return failure
		}
	}
	for _, definition := range generations {
		walkBoundExpression(definition.planned, visit)
		if failure != nil || b.input.TypeDiagnosticName != nil {
			return failure
		}
	}
	return nil
}
func (b *expressionBinder) comparisonError(err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	diagnostic := operatorExpressionError(err, left, right, raw)
	if record, ok := diagnostic.(*sqlstate.Error); ok && record.Code == "42883" {
		copy := *record
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", b.diagnosticTypeName(left), objectNameText(raw.Name), b.diagnosticTypeName(right))
		return &copy
	}
	return diagnostic
}
