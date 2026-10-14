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

// Safety belongs to admission, not discovery of an enum node. Scope inputs and
// standalone binders borrow the same immutable root; candidate mutations clone
// it. Inputs are never retained across admissions, so an existing callback is
// already the current scope's capture and can be shared without another closure.
func captureEnumOperationSafety(op *Operation, input *sqltype.InputEnvironment) {
	if input.EnumMemberSafe != nil || op == nil || op.session == nil || op.session.transactions == nil || op.session.transactions.catalog == nil {
		return
	}
	input.EnumMemberSafe = enumSafety(op.session.transactions.catalog.enumUnsafe)
}

// Capture naming only after the caller supplies its coherent catalog authority.
// Alternate staged binders retain lazy naming through captureTypeDiagnosticInput.
func (e *Engine) captureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	if input.EnumMemberSafe == nil || input.TypeDiagnosticName != nil {
		return nil
	}
	var err error
	input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
	return err
}

func (b *expressionBinder) captureTypeDiagnosticInput(typ *sqltype.Descriptor) error {
	acl := typ.ContainsACLItem() && b.input.ACLItem == nil
	enum := b.input.TypeDiagnosticName == nil && hasEnumInput(typ)
	if !acl && !enum {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	return b.engine.captureTypeDiagnosticInput(auth, b.op, &b.input, typ)
}

func (e *Engine) captureTypeDiagnosticInput(auth AuthorizationView, op *Operation, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	if typ.ContainsACLItem() && input.ACLItem == nil {
		input.ACLItem = e.captureACLItemInput(auth, op)
	}
	if input.TypeDiagnosticName == nil && hasEnumInput(typ) {
		var err error
		input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		return err
	}
	return nil
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

// Retained reads and writes have identical input demands, independent of the
// tree walker used by their consumer. Naming never suppresses catalog input.
func captureRetainedExpressionInput(node BoundExpr, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	switch node := node.(type) {
	case enumFunctionExpression:
		return diagnostic(node.plan.Enum())
	case coercionExpression:
		if sqltype.IsCharacter(node.child.ResultUsage().Type.BaseType().ID()) {
			target := node.ResultUsage().Type
			if target.ContainsCatalogIdentifier() {
				if err := element(); err != nil {
					return err
				}
			}
			return diagnostic(target)
		}
	}
	return nil
}

// Stored sources are opaque to the outer expression binder, but their
// planning-time conversions consume the same published input environment.
func (b *expressionBinder) captureRetainedViewInput(definition *viewDefinition) error {
	return captureRetainedDefinitionInput(b.queryAuth.catalog, definition, b.captureElementInput, b.captureTypeDiagnosticInput)
}

// Each frozen consumer supplies its own coherent catalog and input owners.
func captureRetainedDefinitionInput(catalog CatalogRead, definition *viewDefinition, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	seen := make(map[*viewDefinition]bool)
	var capture func(*viewDefinition) error
	capture = func(definition *viewDefinition) error {
		if seen[definition] {
			return nil
		}
		seen[definition] = true
		var failure error
		walkQueryArgumentNode(definition.query.Root, func(node BoundExpr) {
			if failure == nil {
				failure = captureRetainedExpressionInput(node, element, diagnostic)
			}
		})
		if failure != nil {
			return failure
		}
		for _, source := range definition.sources {
			relation, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
			if ok && relation.relationKind == viewRelation {
				if err := capture(relation.view); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return capture(definition)
}

// Retained write expressions are not rebound with the consuming statement.
// Capture input authority and lazy naming without evaluating defaults or branches.
func (b *expressionBinder) captureRetainedWriteInput(defaults []BoundExpr, generations map[int32]columnGeneration, checks []checkConstraint) error {
	var failure error
	visit := func(node BoundExpr) {
		if failure != nil {
			return
		}
		failure = captureRetainedExpressionInput(node, b.captureElementInput, b.captureTypeDiagnosticInput)
	}
	for _, node := range defaults {
		walkBoundExpression(node, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range generations {
		walkBoundExpression(definition.planned, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range checks {
		walkBoundExpression(definition.expression, visit)
		if failure != nil {
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
