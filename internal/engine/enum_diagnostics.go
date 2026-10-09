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
func (b *expressionBinder) comparisonError(err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	diagnostic := operatorExpressionError(err, left, right, raw)
	if record, ok := diagnostic.(*sqlstate.Error); ok && record.Code == "42883" {
		copy := *record
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", b.diagnosticTypeName(left), objectNameText(raw.Name), b.diagnosticTypeName(right))
		return &copy
	}
	return diagnostic
}
