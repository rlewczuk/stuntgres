package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (b *Binder) DiagnosticTypeName(typ *sqltype.Descriptor) string {
	if typ == nil {
		return "unknown"
	}
	if typ.IsArray() && !typ.IsCatalogVector() {
		return b.DiagnosticTypeName(typ.Element()) + "[]"
	}
	if !typ.IsUserDefined() {
		return typ.DiagnosticName()
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return typ.DiagnosticName()
	}
	definition, _ := b.authorityDescriptor(auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	descriptor, ok := definition.(*catalog.Type)
	if !ok {
		return typ.DiagnosticName()
	}
	path, err := b.Host.ReferencePath(auth)
	if err != nil {
		return typ.DiagnosticName()
	}
	return catalog.TypeDiagnosticNameAt(auth.ReadCatalog(), nil, descriptor, path)
}

func (b *Binder) CaptureTypeDiagnosticInput(typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && b.Input.FTS == nil {
		b.Input.FTS = b.Host.FTSInput()
	}
	acl := typ.ContainsACLItem() && b.Input.ACLItem == nil
	naming := b.Input.TypeDiagnosticName == nil && NeedsTypeDiagnosticInput(typ)
	xml := typ.BaseType().ID() == sqltype.XML && b.Input.XMLInput == nil
	if !acl && !naming && !xml {
		return nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return err
	}
	return b.Host.CaptureTypeDiagnosticInput(auth, &b.Input, typ)
}

// Comparisons need names, not input codecs or role-name resolution. Capture the
// existing naming authority once without acquiring unrelated ACL input state.
func (b *Binder) captureComparisonDiagnosticInput(typ *sqltype.Descriptor) error {
	if b.Input.TypeDiagnosticName != nil || !NeedsTypeDiagnosticInput(typ) {
		return nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return err
	}
	b.Input.TypeDiagnosticName, err = b.Host.CaptureTypeDiagnosticName(auth)
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
	if r := typ.RangeType(); r != nil {
		if definition, custom := r.RangeDefinition(); custom {
			return hasEnumInput(definition.Subtype.Type)
		}
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
func (b *Binder) captureRetainedViewInput(relation *catalog.Relation) error {
	return b.Host.CaptureRetainedViewInput(b.Query.Auth, relation, b.captureElementInput, b.CaptureFTSParserInput, b.CaptureTypeDiagnosticInput)
}

// Retained write expressions are not rebound with the consuming statement.
// Capture input authority, runtime snapshot demand and lazy naming without
// evaluating defaults or branches. Retained recipes bypass ordinary call binding.
func (b *Binder) CaptureRetainedWriteInput(defaults []expr.Expr, generations map[int32]catalog.ColumnGeneration, checks []catalog.CheckConstraint) error {
	var failure error
	visit := func(node expr.Expr) {
		if failure != nil {
			return
		}
		if inquiry, ok := node.(expr.SnapshotDemander); ok {
			_, _, visibility := inquiry.SnapshotDemand(plan.NullLiteral)
			b.Consumer.VisibilitySnapshot = b.Consumer.VisibilitySnapshot || visibility
		}
		if demand, ok := node.(expr.ExportRootDemander); ok {
			b.Consumer.ExportRoots = b.Consumer.ExportRoots || demand.ExportRootDemand()
		}
		failure = CaptureRetainedExpressionInput(node, b.captureElementInput, b.CaptureFTSParserInput, b.CaptureTypeDiagnosticInput)
	}
	return VisitRetainedWriteExpressions(defaults, generations, checks, func(node expr.Expr) error {
		visit(node)
		return failure
	})
}

// VisitRetainedWriteExpressions is the common typed recipe walk for retained
// write input capture and pure executable-demand classification. It performs
// no binding, authorization, catalog capture, or evaluation.
func VisitRetainedWriteExpressions(defaults []expr.Expr, generations map[int32]catalog.ColumnGeneration, checks []catalog.CheckConstraint, visit func(expr.Expr) error) error {
	walk := func(node expr.Expr) error {
		var failure error
		plan.WalkExpression(node, func(child expr.Expr) {
			if failure == nil {
				failure = visit(child)
			}
		})
		return failure
	}
	for _, node := range defaults {
		if err := walk(node); err != nil { return err }
	}
	for _, definition := range generations {
		if err := walk(definition.Planned); err != nil { return err }
	}
	for _, definition := range checks {
		if !definition.NotEnforced {
			if err := walk(definition.Expression); err != nil { return err }
		}
	}
	return nil
}
