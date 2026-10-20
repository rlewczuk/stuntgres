package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) diagnosticTypeName(typ *sqltype.Descriptor) string {
	if typ == nil {
		return "unknown"
	}
	if typ.IsArray() {
		return b.diagnosticTypeName(typ.Element()) + "[]"
	}
	if !typ.IsUserDefined() {
		return typ.DiagnosticName()
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return typ.DiagnosticName()
	}
	definition, _ := b.authorityDescriptor(auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	descriptor, ok := definition.(*catalog.Type)
	if !ok {
		return typ.DiagnosticName()
	}
	path, err := b.host.ReferencePath(auth)
	if err != nil {
		return typ.DiagnosticName()
	}
	return catalog.TypeDiagnosticNameAt(auth.ReadCatalog(), descriptor, path)
}

func (b *expressionBinder) captureTypeDiagnosticInput(typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && b.input.FTS == nil {
		b.input.FTS = b.host.FTSInput()
	}
	acl := typ.ContainsACLItem() && b.input.ACLItem == nil
	naming := b.input.TypeDiagnosticName == nil && analyze.NeedsTypeDiagnosticInput(typ)
	if !acl && !naming {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	return b.host.CaptureTypeDiagnosticInput(auth, &b.input, typ)
}

// Comparisons need names, not input codecs or role-name resolution. Capture the
// existing naming authority once without acquiring unrelated ACL input state.
func (b *expressionBinder) captureComparisonDiagnosticInput(typ *sqltype.Descriptor) error {
	if b.input.TypeDiagnosticName != nil || !analyze.NeedsTypeDiagnosticInput(typ) {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	b.input.TypeDiagnosticName, err = b.host.CaptureTypeDiagnosticName(auth)
	return err
}

// Input provenance and diagnostic naming are separate demands. Only enum
// inputs and checked domains retain the existing replacement fence here.
func hasEnumInput(typ *sqltype.Descriptor) bool {
	if typ == nil {
		return false
	}
	if typ.HasDomainConstraints() {
		return true
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

// Stored sources are opaque to the outer expression binder, but their
// planning-time conversions consume the same published input environment.
func (b *expressionBinder) captureRetainedViewInput(relation *catalog.Relation) error {
	return b.host.CaptureRetainedViewInput(b.query.auth, relation, b.captureElementInput, b.captureTypeDiagnosticInput)
}

// Retained write expressions are not rebound with the consuming statement.
// Capture input authority and lazy naming without evaluating defaults or branches.
func (b *expressionBinder) captureRetainedWriteInput(defaults []expr.Expr, generations map[int32]catalog.ColumnGeneration, checks []catalog.CheckConstraint) error {
	var failure error
	visit := func(node expr.Expr) {
		if failure != nil {
			return
		}
		failure = analyze.CaptureRetainedExpressionInput(node, b.captureElementInput, b.captureTypeDiagnosticInput)
	}
	for _, node := range defaults {
		plan.WalkExpression(node, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range generations {
		plan.WalkExpression(definition.Planned, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range checks {
		plan.WalkExpression(definition.Expression, visit)
		if failure != nil {
			return failure
		}
	}
	return nil
}
